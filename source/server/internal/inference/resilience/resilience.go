// Package resilience wraps a primary (and optionally one backup) cloud
// provider in a single inference.Provider that owns the ENTIRE retry/failover
// policy for cloud calls. Adapters normalize their wire errors into the
// llm.Error taxonomy and hand them up; this engine decides — per class —
// whether to retry the same provider, fail over to the backup, or surface the
// error, and it narrates every action to the user (in-band EventNotice on
// streams, OnEvent hook for logs and non-streaming calls).
//
// There is deliberately no other retry layer in the stack: SDK-internal
// retries are disabled and the transport-level httpx retry was removed. A
// retry that happens below this engine is invisible to its policy and to the
// user — the failure mode behind the 2026-07-16 quota incident, where the
// Anthropic SDK silently slept on a quota-scale Retry-After and failover
// never saw an error.
//
// Policy by class:
//
//	busy            → one narrated same-provider retry (wait = server's
//	                  Retry-After capped at RetryWaitCap, default 500ms),
//	                  then fail over / surface
//	quota          → immediate failover, then skip the primary until the
//	                  Retry-After/default cooldown expires
//	auth,
//	network, unknown → immediate failover (retrying is pointless: the
//	                  condition will not heal within a turn)
//	invalid_request → surface (the request is wrong everywhere)
//
// Context cancellation always surfaces — the caller gave up.
//
// The wrapper is built even when no backup is configured: the busy retry and
// the narration still apply; failover steps are simply skipped.
package resilience

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// Action is what the engine decided to do about a primary failure.
type Action string

const (
	ActionRetry    Action = "retry"    // same provider, once, after a short wait
	ActionFailover Action = "failover" // re-serve on the backup
	ActionSurface  Action = "surface"  // give up; the error goes to the caller
)

// Event describes one engine decision, for logging and telemetry. The Notice
// text (the user-facing line) is derived from the same data via Notice().
type Event struct {
	Action Action
	Stage  string // "chat" | "stream_dial" | "stream_first"
	Class  llm.ErrorClass
	From   string        // provider that failed
	To     string        // provider serving next ("" on surface)
	Wait   time.Duration // retry only: how long the engine waits first
	Err    error         // the failure that triggered the decision
}

// Notice renders the user-facing status line for an event, in the fixed
// house style: "<provider> <what happened> — <what we're doing>".
func (e Event) Notice() string {
	what := map[llm.ErrorClass]string{
		llm.ErrQuota:           "quota reached",
		llm.ErrBusy:            "server busy",
		llm.ErrAuth:            "auth failed",
		llm.ErrNetwork:         "unreachable",
		llm.ErrInvalidRequest:  "rejected the request",
		llm.ErrContextOverflow: "context window exceeded",
		llm.ErrUnknown:         "failed",
	}[e.Class]
	if what == "" {
		what = "failed"
	}
	switch e.Action {
	case ActionRetry:
		return fmt.Sprintf("%s %s — trying once more", e.From, what)
	case ActionFailover:
		if e.Class == llm.ErrBusy {
			// The busy failover always follows a failed retry.
			return fmt.Sprintf("%s still busy — switching to %s", e.From, e.To)
		}
		return fmt.Sprintf("%s %s — switching to %s", e.From, what, e.To)
	default:
		return fmt.Sprintf("%s %s", e.From, what)
	}
}

// Options configures the engine. Zero values give: no backup, silent events,
// 500ms default retry wait, 2s cap, 1h quota cooldown.
type Options struct {
	PrimaryBlocked     bool // credential/configuration failures must reach their gate, not automatic fallback
	PrimaryUnavailable bool // construction failure, excluding actionable credential failures
	// PrimaryModelFor maps a capability-tier name to the primary vendor's model
	// for that tier. When set, tiered requests are normalized before the first
	// primary attempt, so a stale or foreign request Model cannot leak across
	// cloud vendors (for example gpt-* into Anthropic). Untiered requests keep
	// their explicit Model.
	PrimaryModelFor func(tier string) string
	// Backup, when non-nil, serves calls the primary failed in a way a
	// different vendor could plausibly serve.
	Backup inference.Provider
	// BackupLabel identifies the configured profile in authentication prompts.
	BackupLabel string
	// BackupModelFor maps a capability-tier name to the backup vendor's model
	// for that tier (experience-preserving rewrite); called with "" for
	// untiered requests, where it must return the backup profile's default
	// model. Required when Backup is set.
	BackupModelFor func(tier string) string
	// OnEvent observes every engine decision (for the server log). May be nil.
	OnEvent func(Event)
	// RetryWait is the busy-retry wait when the server suggested none.
	RetryWait time.Duration
	// RetryWaitCap bounds a server-suggested Retry-After. A quota-scale value
	// never reaches here (quota classifies to immediate failover), but a
	// hostile or odd busy value must not stall the turn.
	RetryWaitCap time.Duration
	// QuotaCooldown is how long the primary is skipped after a quota failover
	// when the provider did not supply Retry-After. Zero defaults to 1 hour.
	QuotaCooldown time.Duration
}

const (
	defaultRetryWait     = 500 * time.Millisecond
	defaultRetryWaitCap  = 2 * time.Second
	defaultQuotaCooldown = time.Hour
)

// Provider is the engine. It impersonates the primary everywhere except the
// moment of a decision, which is narrated via EventNotice / OnEvent.
type Provider struct {
	primaryBlocked     bool
	primaryUnavailable bool
	primary            inference.Provider
	primaryModelFor    func(tier string) string
	backup             inference.Provider
	backupLabel        string
	backupModelFor     func(tier string) string
	onEvent            func(Event)
	retryWait          time.Duration
	retryWaitCap       time.Duration
	quotaCooldown      time.Duration
	// sleep and now are injection seams for tests; production uses ctx-aware
	// sleep and the wall clock.
	sleep func(ctx context.Context, d time.Duration) bool
	now   func() time.Time

	mu                 sync.Mutex
	quotaCooldownUntil time.Time
}

// New builds the engine around primary.
func New(primary inference.Provider, opts Options) *Provider {
	p := &Provider{
		primaryUnavailable: opts.PrimaryUnavailable,
		primaryBlocked:     opts.PrimaryBlocked,
		primary:            primary,
		primaryModelFor:    opts.PrimaryModelFor,
		backup:             opts.Backup,
		backupLabel:        opts.BackupLabel,
		backupModelFor:     opts.BackupModelFor,
		onEvent:            opts.OnEvent,
		retryWait:          opts.RetryWait,
		retryWaitCap:       opts.RetryWaitCap,
		quotaCooldown:      opts.QuotaCooldown,
		sleep:              ctxSleep,
		now:                time.Now,
	}
	if p.retryWait <= 0 {
		p.retryWait = defaultRetryWait
	}
	if p.retryWaitCap <= 0 {
		p.retryWaitCap = defaultRetryWaitCap
	}
	if p.quotaCooldown <= 0 {
		p.quotaCooldown = defaultQuotaCooldown
	}
	return p
}

func ctxSleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (p *Provider) Name() string {
	if p.primaryUnavailable && p.backup != nil {
		return p.backup.Name()
	}
	return p.primary.Name()
}

func (p *Provider) Capabilities() inference.Capabilities {
	if p.primaryUnavailable && p.backup != nil {
		return p.backup.Capabilities()
	}
	return p.primary.Capabilities()
}

func (p *Provider) emit(ev Event) {
	if p.onEvent != nil {
		p.onEvent(ev)
	}
}

func eventFrom(fallback string, err error) string {
	if name := llm.ProviderOf(err); name != "" {
		return name
	}
	return fallback
}

// waitFor computes the busy-retry wait: the server's suggestion capped, or
// the default when it gave none.
func (p *Provider) waitFor(err error) time.Duration {
	var ne *llm.Error
	if errors.As(err, &ne) && ne.RetryAfter > 0 {
		if ne.RetryAfter < p.retryWaitCap {
			return ne.RetryAfter
		}
		return p.retryWaitCap
	}
	return p.retryWait
}

func (p *Provider) quotaCooldownFor(err error) time.Duration {
	var ne *llm.Error
	if errors.As(err, &ne) && ne.RetryAfter > 0 {
		return ne.RetryAfter
	}
	return p.quotaCooldown
}

func (p *Provider) markQuotaCooldown(err error) {
	until := p.now().Add(p.quotaCooldownFor(err))
	p.mu.Lock()
	defer p.mu.Unlock()
	if until.After(p.quotaCooldownUntil) {
		p.quotaCooldownUntil = until
	}
}

func (p *Provider) quotaCoolingDown() bool {
	if p.backup == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.now().Before(p.quotaCooldownUntil)
}

// primaryRequest rewrites tiered requests into the primary provider's model
// namespace before the first attempt. This is intentionally symmetric with
// backupRequest: callers may carry a stale Model string from a previous active
// profile, but Tier is the provider-neutral intent. Untiered requests keep
// their explicit Model so one-off overrides still work.
func (p *Provider) primaryRequest(req inference.Call) inference.Call {
	if p.primaryModelFor == nil || req.Tier == "" {
		return req
	}
	req.Model = p.primaryModelFor(req.Tier)
	return req
}

// backupRequest rewrites the request into the backup provider's model
// namespace, preserving the request's capability tier when it carries one.
func (p *Provider) backupRequest(req inference.Call) inference.Call {
	if req.FallbackTier != "" {
		req.Tier = req.FallbackTier
		req.FallbackTier = ""
	}
	if p.backupModelFor == nil {
		return req
	}
	if model := p.backupModelFor(req.Tier); model != "" {
		req.Model = model
	}
	return req
}

// Chat runs the non-streaming policy. Notices reach logs via OnEvent only —
// there is no user-visible stream on this path.
func (p *Provider) Chat(ctx context.Context, req inference.Call) (inference.Result, error) {
	req = p.primaryRequest(req)
	attempts := make(map[string]bool)
	if llm.AuthFallbackSelected(ctx, p) {
		if !p.authenticationBackupAllowed(req) {
			return inference.Result{}, incompatibleAuthFallback()
		}
		r, e, _ := p.chatAuth(ctx, p.backup, p.backupRequest(req), false, attempts)
		return r, llm.SelectedAuthFallbackFailure(e)
	}
	if p.useBackup(req) {
		r, e, _ := p.chatAuth(ctx, p.backup, p.backupRequest(req), false, attempts)
		return r, e
	}
	if req.Tier != "" && p.primaryModelFor != nil && req.Model == "" {
		return inference.Result{}, fmt.Errorf("primary model unavailable for tier %q", req.Tier)
	}
	resp, err, terminal := p.chatAuth(ctx, p.primary, req, true, attempts)
	if terminal {
		return resp, err
	}
	if err == nil || ctx.Err() != nil {
		return resp, err
	}
	if llm.Retryable(llm.ClassOf(err)) {
		ev := Event{Action: ActionRetry, Stage: "chat", Class: llm.ClassOf(err),
			From: eventFrom(p.primary.Name(), err), To: p.primary.Name(), Wait: p.waitFor(err), Err: err}
		p.emit(ev)
		if !p.sleep(ctx, ev.Wait) {
			return resp, err
		}
		resp, err, terminal = p.chatAuth(ctx, p.primary, req, true, attempts)
		if terminal {
			return resp, err
		}
		if err == nil || ctx.Err() != nil {
			return resp, err
		}
	}
	class := llm.ClassOf(err)
	if p.backup == nil || !llm.Failoverable(class, err) {
		p.emit(Event{Action: ActionSurface, Stage: "chat", Class: class,
			From: eventFrom(p.primary.Name(), err), Err: err})
		return inference.Result{}, err
	}
	if class == llm.ErrQuota {
		p.markQuotaCooldown(err)
	}
	p.emit(Event{Action: ActionFailover, Stage: "chat", Class: class,
		From: eventFrom(p.primary.Name(), err), To: p.backup.Name(), Err: err})
	r, e, _ := p.chatAuth(ctx, p.backup, p.backupRequest(req), false, attempts)
	return r, e
}

// StreamChat runs the streaming policy. Decisions are narrated in-band: the
// reader yields an EventNotice describing the action, then performs it on the
// following Next() — so the UI shows "trying once more" while the engine
// waits, not after.
func (p *Provider) StreamChat(ctx context.Context, req inference.Call) (inference.Stream, error) {
	req = p.primaryRequest(req)
	if !p.useBackup(req) && req.Tier != "" && p.primaryModelFor != nil && req.Model == "" {
		return nil, fmt.Errorf("primary model unavailable for tier %q", req.Tier)
	}
	if llm.AuthFallbackSelected(ctx, p) && !p.authenticationBackupAllowed(req) {
		return nil, incompatibleAuthFallback()
	}
	r := &reader{ctx: ctx, p: p, req: p.primaryRequest(req), authAttempts: make(map[string]bool), failedOver: p.useBackup(req) || llm.AuthFallbackSelected(ctx, p), authFallback: llm.AuthFallbackSelected(ctx, p)}
	inner, err := r.open()
	if err != nil {
		if !r.decide("stream_dial", err) {
			return nil, r.failure(err)
		}
		return r, nil
	}
	r.inner = inner
	return r, nil
}

// reader is the streaming state machine. Until a real event has flowed it can
// recover from failures (one busy retry, then failover); once content has
// been delivered a failure stays an error — silently re-serving would
// duplicate already-delivered text. After a failover it never cascades.
type reader struct {
	ctx context.Context
	p   *Provider
	req inference.Call

	inner llm.StreamReader // nil while an attempt is pending

	queue   []llm.StreamEvent                // injected notices to deliver first
	attempt func() (llm.StreamReader, error) // deferred action set by decide()

	emitted      bool // a real event was delivered; recovery is off the table
	retried      bool // the one busy retry has been used
	failedOver   bool // already on the backup; never cascade
	authAttempts map[string]bool
	terminalErr  error
	authFallback bool
}

func (r *reader) Next() (llm.StreamEvent, bool, error) {
	for {
		if len(r.queue) > 0 {
			ev := r.queue[0]
			r.queue = r.queue[1:]
			return ev, true, nil
		}
		if r.attempt != nil {
			act := r.attempt
			r.attempt = nil
			inner, err := act()
			if err != nil {
				if r.decide("stream_dial", err) {
					continue
				}
				return llm.StreamEvent{}, false, r.failure(err)
			}
			r.inner = inner
			continue
		}
		if r.inner == nil {
			return llm.StreamEvent{}, false, nil
		}
		ev, ok, err := r.inner.Next()
		authErr := err
		if authErr == nil && ok && ev.Type == llm.EventError {
			authErr = ev.Err
		}
		if llm.ClassOf(authErr) == llm.ErrLoginRequired {
			if r.decide("stream_auth", authErr) {
				continue
			}
			return llm.StreamEvent{}, false, r.failure(authErr)
		}
		if r.emitted || r.failedOver {
			if r.authFallback {
				if err != nil {
					return llm.StreamEvent{}, false, r.failure(err)
				}
				if ok && ev.Type == llm.EventError {
					return llm.StreamEvent{}, false, r.failure(ev.Err)
				}
			}
			return ev, ok, err
		}
		switch {
		case err != nil:
			if r.decide("stream_first", err) {
				continue
			}
			return llm.StreamEvent{}, false, r.failure(err)
		case ok && ev.Type == llm.EventError:
			streamErr := ev.Err
			if streamErr == nil {
				// Legacy adapters may still send only ErrText. Preserve previous
				// behavior for those frames: classify as unknown and let the shared
				// policy decide whether a recovery attempt is worthwhile.
				streamErr = &llm.Error{Class: llm.ErrUnknown,
					Provider: r.p.primary.Name(), Err: errors.New(ev.ErrText)}
			}
			if r.decide("stream_first", streamErr) {
				continue
			}
			return llm.StreamEvent{}, false, r.failure(streamErr)
		default:
			// First real event (or a clean immediate end): the stream is live.
			r.emitted = true
			return ev, ok, err
		}
	}
}

// decide runs the per-class policy for a pre-content failure. It returns true
// when it scheduled a recovery (notice queued + attempt set) and false when
// the error must surface.
func (r *reader) decide(stage string, err error) bool {
	if r.ctx.Err() != nil {
		return false
	}
	if r.inner != nil {
		_ = r.inner.Close()
		r.inner = nil
	}
	class := llm.ClassOf(err)
	p := r.p
	if class == llm.ErrLoginRequired {
		fallback := ""
		if !r.failedOver && p.authenticationBackupAllowed(r.req) {
			fallback = p.authenticationBackupName()
		}
		choice, recoveryErr := requestAuth(r.ctx, err, fallback, !r.emitted, r.authAttempts)
		if recoveryErr != nil {
			r.terminalErr = recoveryErr
			return false
		}
		switch choice {
		case llm.AuthLogin:
			if r.emitted {
				r.terminalErr = &llm.CredentialError{Class: llm.ErrCredential, Reason: "login completed; partial response requires an explicit fresh request"}
				return false
			}
			r.attempt = r.open
			return true
		case llm.AuthFallback:
			if !r.failedOver && p.authenticationBackupAllowed(r.req) {
				r.failedOver = true
				r.authFallback = true
				llm.SelectAuthFallback(r.ctx, p)
				r.ctx = llm.WithExternalAuthFallback(r.ctx, "")
				r.attempt = r.open
				return true
			}
			r.terminalErr = &llm.AuthFallbackRequest{Cause: err}
			return false
		}
		return false
	}
	if r.failedOver {
		return false
	}
	if llm.Retryable(class) && !r.retried {
		r.retried = true
		ev := Event{Action: ActionRetry, Stage: stage, Class: class,
			From: eventFrom(p.primary.Name(), err), To: p.primary.Name(), Wait: p.waitFor(err), Err: err}
		p.emit(ev)
		r.queue = append(r.queue, llm.StreamEvent{Type: llm.EventNotice, Notice: ev.Notice()})
		r.attempt = func() (llm.StreamReader, error) {
			if !p.sleep(r.ctx, ev.Wait) {
				return nil, err
			}
			return p.primary.StreamChat(r.ctx, r.req)
		}
		return true
	}
	if p.backup != nil && llm.Failoverable(class, err) && !r.failedOver {
		r.failedOver = true
		if class == llm.ErrQuota {
			p.markQuotaCooldown(err)
		}
		ev := Event{Action: ActionFailover, Stage: stage, Class: class,
			From: eventFrom(p.primary.Name(), err), To: p.backup.Name(), Err: err}
		p.emit(ev)
		r.queue = append(r.queue, llm.StreamEvent{Type: llm.EventNotice, Notice: ev.Notice()})
		r.attempt = func() (llm.StreamReader, error) {
			return p.backupStream(r.ctx, r.req)
		}
		return true
	}
	p.emit(Event{Action: ActionSurface, Stage: stage, Class: class,
		From: eventFrom(p.primary.Name(), err), Err: err})
	return false
}

func (r *reader) Close() error {
	if r.inner != nil {
		return r.inner.Close()
	}
	return nil
}

func (r *reader) open() (llm.StreamReader, error) {
	if r.failedOver {
		return r.p.backupStream(r.ctx, r.req)
	}
	return r.p.primary.StreamChat(r.ctx, r.req)
}
func (r *reader) failure(err error) error {
	if r.terminalErr != nil {
		return r.terminalErr
	}
	if r.authFallback {
		return llm.SelectedAuthFallbackFailure(err)
	}
	return err
}

func (p *Provider) authenticationBackupName() string {
	if p.backupLabel != "" {
		return p.backupLabel + " (" + p.backup.Name() + ")"
	}
	return p.backup.Name()
}
