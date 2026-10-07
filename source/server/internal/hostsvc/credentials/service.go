// Package credentials owns credential refresh and replacement for the host.
// Provider adapters and workers receive lightweight views of the same service;
// keychain persistence remains in secrets.Store. No network operation holds a
// profile's mutation lock, and stale refresh results cannot overwrite a login.
package credentials

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"cercano/source/server/internal/anthropicauth"
	"cercano/source/server/internal/chatgptauth"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/secrets"
)

const refreshTimeout = 30 * time.Second

// ErrAdmission marks a refresh that was refused by the update admission gate
// BEFORE it started. It is deliberately not an llm.CredentialError: a paused
// update is a transient host state, not an invalid or revoked credential, and
// callers must not treat it as an authentication failure, revoke anything, or
// write anything. errors.Is(err, ErrAdmission) is the only intended match.
var ErrAdmission = errors.New("credential refresh paused by update admission")

// Service is shared by config, login handlers, host providers and workers. Its
// Store facade routes all host writes through the same generation boundary.
// Do not retain or mutate its raw backing store after handing it to this owner.
type Service struct {
	mu       sync.Mutex
	store    secrets.Store
	profiles map[string]*profileState

	// activityMu guards the update-admission tracking of refresh flights. It
	// is deliberately independent of mu and every profileState.mu: no path
	// acquires mu or a profile lock while holding activityMu, so admission
	// bookkeeping can never invert against credential mutation. The admission
	// callbacks may be invoked while the caller that spawned a flight still
	// holds a profile lock, so they must not reenter this service.
	activityMu  sync.Mutex
	admission   func() (func(), error)
	liveFlights map[*flight]func()
}
type profileState struct {
	mu             sync.Mutex
	store          secrets.Store
	generation     uint64
	flight         *flight
	login          *LoginAttempt
	loginCompleted map[string]chan struct{}
}
type token struct{ access, account string }
type flight struct {
	provider   string
	generation uint64
	waiters    int
	done       chan struct{}
	exited     chan struct{}
	cancel     context.CancelFunc
	superseded bool
	abandoned  bool
	value      token
	err        error
}

func New(store secrets.Store) *Service {
	return &Service{store: store, profiles: make(map[string]*profileState), liveFlights: make(map[*flight]func())}
}

// BindWorkAdmission installs a startup-only work admission hook covering every
// credential refresh flight. Acquire is called synchronously BEFORE a refresh
// is spawned; its release runs when the refresh goroutine has actually
// returned — after it committed (or skipped) its rotated-token write and
// unlocked the profile — so a waiter that returns early on its own
// cancellation never releases the shared refresh's lease.
//
// Flights already running when Bind is called are retroactively leased so the
// gate counts them too. Retroactive leasing works from the flight identity
// map alone: no profile name, token, or secret value is read or passed to the
// hook. If any acquire fails or returns a nil release, the leases taken during
// this bind are undone (released) and the hook is left unconfigured, so a
// failed bind never leaves a partially tracked service. Rebinding and a nil
// acquire are refused. The hook must not reenter this service; it observes
// only its own admission gate.
func (s *Service) BindWorkAdmission(acquire func() (func(), error)) error {
	if acquire == nil {
		return errors.New("nil credential work admission")
	}
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	if s.admission != nil {
		return errors.New("credential work admission already bound")
	}
	// Probe the hook once so a nil release (or a sealed gate) is rejected AT
	// BIND TIME rather than on the first refresh: the probe lease is retired
	// immediately and is never attached to a flight. The hook must not reenter
	// this service.
	probe, err := acquire()
	if err != nil || probe == nil {
		if err != nil {
			return fmt.Errorf("credential work admission: %w", err)
		}
		return errors.New("credential work admission returned no release")
	}
	probe()
	if s.liveFlights == nil {
		s.liveFlights = make(map[*flight]func())
	}
	acquired := make(map[*flight]func(), len(s.liveFlights))
	for f, release := range s.liveFlights {
		if release != nil {
			continue // defensive: a flight is never leased before the hook exists
		}
		lease, err := acquire()
		if err != nil || lease == nil {
			// Undo: retire every lease taken during this bind and mark those
			// flights unleased again so a later bind can lease them cleanly.
			for f, undo := range acquired {
				s.liveFlights[f] = nil
				undo()
			}
			if err != nil {
				return fmt.Errorf("credential work admission: %w", err)
			}
			return errors.New("credential work admission returned no release")
		}
		acquired[f] = lease
		s.liveFlights[f] = lease
	}
	s.admission = acquire
	return nil
}

// beginFlightActivity acquires one admitted lifetime for a refresh flight
// before that flight is spawned. With no hook installed it returns a nil
// release and the refresh runs untracked (the pre-hook behavior). An admission
// refusal — or a hook returning a nil release — is an error: the refresh must
// not start. The error is the typed ErrAdmission wrapper, never a credential
// classification, and nothing has been written.
//
// The admission callback runs under activityMu so a concurrent BindWorkAdmission
// cannot interleave (either the flight is in the map before the bind's
// retroactive walk sees it, or it observes the freshly bound hook). That is
// safe because the hook must not reenter this service.
func (s *Service) beginFlightActivity() (func(), error) {
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	if s.admission == nil {
		return nil, nil
	}
	release, err := s.admission()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAdmission, err)
	}
	if release == nil {
		return nil, fmt.Errorf("%w: admission returned no release", ErrAdmission)
	}
	return release, nil
}

// retainFlight records the flight as live BEFORE it is spawned, so the lease
// already exists when any other goroutine can observe the flight.
func (s *Service) retainFlight(f *flight, release func()) {
	s.activityMu.Lock()
	if s.liveFlights == nil {
		s.liveFlights = make(map[*flight]func())
	}
	s.liveFlights[f] = release
	s.activityMu.Unlock()
}

// releaseFlight retires the flight's admitted lifetime. It is deferred by the
// refresh goroutine itself and runs only after that goroutine has committed
// (or skipped) its rotated-token write, unlocked the profile, and actually
// returned — so superseded and abandoned flights stay tracked for their whole
// real lifetime even after profileState.flight has moved on.
func (s *Service) releaseFlight(f *flight) {
	s.activityMu.Lock()
	release := s.liveFlights[f]
	delete(s.liveFlights, f)
	s.activityMu.Unlock()
	if release != nil {
		release()
	}
}
func (s *Service) profile(name string) *profileState {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.profiles[name]
	if p == nil {
		p = &profileState{store: s.store, loginCompleted: make(map[string]chan struct{})}
		s.profiles[name] = p
	}
	return p
}

// ReplaceStore keeps the service identity stable during host reconfiguration.
// Old adapters remain views of this service rather than of a retired backend.
func (s *Service) ReplaceStore(store secrets.Store) {
	if store == s {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store = store
	for _, p := range s.profiles {
		p.mu.Lock()
		p.store = store
		p.invalidate()
		p.mu.Unlock()
	}
}

// invalidate is called with the profile mutation lock held. Wake waiters so
// they can resolve the new generation immediately, even if an old transport is
// slow to honor cancellation. That transport can no longer commit its result.
func (p *profileState) invalidate() {
	p.cancelLogin()
	p.generation++
	if f := p.flight; f != nil {
		p.flight = nil
		f.superseded = true
		f.cancel()
		close(f.done)
	}
}

var errNoStore = errors.New("credential store unavailable")

func (s *Service) Get(name string) (string, error) {
	p := s.profile(name)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.store == nil {
		return "", errNoStore
	}
	return p.store.Get(name)
}
func (s *Service) Set(name, value string) error {
	p := s.profile(name)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.store == nil {
		return errNoStore
	}
	if err := p.store.Set(name, value); err != nil {
		return err
	}
	p.invalidate()
	return nil
}
func (s *Service) Delete(name string) error {
	p := s.profile(name)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.store == nil {
		return errNoStore
	}
	if err := p.store.Delete(name); err != nil {
		return err
	}
	p.invalidate()
	return nil
}
func (s *Service) List() ([]string, error) {
	s.mu.Lock()
	store := s.store
	s.mu.Unlock()
	if store == nil {
		return nil, errNoStore
	}
	return store.List()
}

// transaction stages the legacy source's write until the service verifies its
// generation. Sources retain provider-specific parsing/refresh logic but never
// receive the raw backing store or independently commit refreshed credentials.
type transaction struct {
	profile, raw string
	loadErr      error
	staged       *string
}

func (t *transaction) Get(profile string) (string, error) {
	if profile != t.profile {
		return "", errors.New("credential transaction profile mismatch")
	}
	return t.raw, t.loadErr
}
func (t *transaction) Set(profile, value string) error {
	if profile != t.profile {
		return errors.New("credential transaction profile mismatch")
	}
	t.staged = &value
	return nil
}

type resolve func(context.Context, *transaction) (token, error)

func (s *Service) acquire(ctx context.Context, profile, provider string, resolve resolve) (token, error) {
	p := s.profile(profile)
	// Repeated replacement is bounded; no automatic retry of rejected grants.
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return token{}, err
		}
		p.mu.Lock()
		f := p.flight
		if f != nil && f.provider != provider {
			p.mu.Unlock()
			return token{}, &llm.CredentialError{Class: llm.ErrCredential, Provider: provider, Profile: profile, Method: llm.AuthSubscription, Reason: "provider_mismatch"}
		}
		if f == nil {
			snapshot := &transaction{profile: profile}
			if p.store == nil {
				snapshot.loadErr = errNoStore
			} else {
				snapshot.raw, snapshot.loadErr = p.store.Get(profile)
			}
			// Admission is acquired BEFORE the refresh exists: a refusal must
			// start no refresh, write nothing, and return a typed error that
			// is never classified as an invalid or revoked credential. The
			// hook may be invoked while this profile lock is held, so it must
			// not reenter the service.
			releaseActivity, err := s.beginFlightActivity()
			if err != nil {
				p.mu.Unlock()
				return token{}, err
			}
			// A waiter owns only its cancellation, not the shared refresh. The last
			// waiter cancels the bounded refresh context; other waiters stay live.
			workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
			f = &flight{provider: provider, generation: p.generation, done: make(chan struct{}), exited: make(chan struct{}), cancel: cancel}
			// Retain the flight before spawning so its lease is already visible
			// to BindWorkAdmission and the gate from the moment it can run.
			s.retainFlight(f, releaseActivity)
			p.flight = f
			go s.run(workCtx, p, f, snapshot, resolve)
		}
		f.waiters++
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			p.mu.Lock()
			f.waiters--
			if p.flight == f && f.waiters == 0 {
				f.abandoned = true
				f.cancel()
			}
			p.mu.Unlock()
			return token{}, ctx.Err()
		case <-f.done:
			if err := ctx.Err(); err != nil {
				return token{}, err
			}
			if f.superseded || f.abandoned {
				continue
			}
			return f.value, f.err
		}
	}
	return token{}, &llm.CredentialError{Class: llm.ErrCredential, Provider: provider, Profile: profile, Method: llm.AuthSubscription, Reason: "replaced_repeatedly"}
}

func (s *Service) run(ctx context.Context, p *profileState, f *flight, snapshot *transaction, resolve resolve) {
	// The admitted lifetime is released only after this goroutine has actually
	// returned — after the commit section below unlocked the profile — so the
	// lease outlives every waiter, including ones that already returned on
	// their own cancellation. Registered first so it runs last.
	defer s.releaseFlight(f)
	defer f.cancel()
	defer close(f.exited)
	value, err := resolve(ctx, snapshot)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.flight != f || p.generation != f.generation {
		return
	}
	if err == nil && snapshot.staged != nil {
		if writeErr := p.store.Set(snapshot.profile, *snapshot.staged); writeErr != nil {
			err = &llm.CredentialError{Class: llm.ErrCredential, Provider: f.provider, Profile: snapshot.profile, Method: llm.AuthSubscription, Reason: llm.CredentialStore, Cause: writeErr}
		} else {
			p.generation++
		}
	}
	if err != nil {
		value = token{}
	}
	f.value, f.err = value, err
	p.flight = nil
	close(f.done)
}

// AnthropicSource and ChatGPTSource are uncached views: rebuilding a provider
// cannot create a second credential owner or retain obsolete credentials.
type AnthropicSource struct {
	service *Service
	profile string
	flow    anthropicauth.Flow
}

func (s *Service) Anthropic(profile string, flow anthropicauth.Flow) *AnthropicSource {
	return &AnthropicSource{s, profile, flow}
}
func (s *AnthropicSource) CredentialProfile() string { return s.profile }
func (s *AnthropicSource) Token(ctx context.Context) (string, error) {
	value, err := s.service.acquire(ctx, s.profile, "anthropic", func(ctx context.Context, t *transaction) (token, error) {
		access, err := anthropicauth.NewSource(t, s.profile, s.flow).Token(ctx)
		return token{access: access}, err
	})
	return value.access, err
}

type ChatGPTSource struct {
	service *Service
	profile string
	flow    chatgptauth.Flow
}

func (s *Service) ChatGPT(profile string, flow chatgptauth.Flow) *ChatGPTSource {
	return &ChatGPTSource{s, profile, flow}
}
func (s *ChatGPTSource) CredentialProfile() string { return s.profile }
func (s *ChatGPTSource) Token(ctx context.Context) (string, string, error) {
	value, err := s.service.acquire(ctx, s.profile, "openai-responses", func(ctx context.Context, t *transaction) (token, error) {
		access, account, err := chatgptauth.NewSource(t, s.profile, s.flow).Token(ctx)
		return token{access, account}, err
	})
	return value.access, value.account, err
}

var _ secrets.Store = (*Service)(nil)
