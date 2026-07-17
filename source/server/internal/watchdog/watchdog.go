package watchdog

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// Mode controls how the Watchdog responds to a first-time violation.
type Mode string

const (
	ModeChallenge Mode = "challenge-and-justify"
	ModeStrict    Mode = "strict"
)

// Decision is the result returned by Gate.
type Decision struct {
	Action    string // "allow" | "challenge" | "block" | "escalate"
	Protocol  string
	Challenge string
	// Revise carries the violated check's corrective instruction (Verdict.Revise).
	Revise string
}

// Config tunes Watchdog behaviour.
// EscalateAfter: number of times the same key must be seen before escalation.
// Zero means default (2).
// CheckTimeout bounds each Gate call's check evaluation. Zero means default
// (15s). Supervision must never wedge the supervised: a turn-end gate runs
// after the reply has already streamed, so an unbounded check against a sick
// model lane holds the stream open and the client queues everything.
type Config struct {
	Mode           Mode
	EscalateAfter  int
	CheckTimeout   time.Duration
	Audit          AuditLogger
	AuditPrompts   bool
	AuditResponses bool
}

// convState holds per-conversation state.
type convState struct {
	justified      map[string]bool
	counts         map[string]int
	lastChallenged string // key most recently challenged or escalated for this conversation
}

// Watchdog runs a set of Checks against agent Actions and enforces a
// per-conversation challenge/block/escalate policy.
type Watchdog struct {
	cfg           Config
	checks        []Check
	oneShot       OneShotFunc
	escalateAfter int
	checkTimeout  time.Duration

	mu    sync.Mutex
	convs map[string]*convState

	echo func(thread, text string) // optional; nil means silent
}

// SetEcho registers a callback that is called on watchdog interventions and
// justify overrides. thread is "watchdog" or "main". Synchronized with w.mu,
// so it may be called on a live Watchdog; note the callback is global to the
// Watchdog, not per-conversation — under concurrent conversations, echo lines
// from all of them reach the same callback.
func (w *Watchdog) SetEcho(fn func(thread, text string)) {
	w.mu.Lock()
	w.echo = fn
	w.mu.Unlock()
}

// emitEcho calls the echo callback if one is registered. It briefly acquires
// w.mu to snapshot the callback, so callers must NOT hold w.mu (sync.Mutex is
// not reentrant). The callback itself runs outside the lock, so it can never
// deadlock the watchdog.
func (w *Watchdog) emitEcho(thread, text string) {
	w.mu.Lock()
	fn := w.echo
	w.mu.Unlock()
	if fn != nil {
		fn(thread, text)
	}
}

// New creates a ready Watchdog.
func New(cfg Config, checks []Check, oneShot OneShotFunc) *Watchdog {
	ea := cfg.EscalateAfter
	if ea <= 0 {
		ea = 2
	}
	ct := cfg.CheckTimeout
	if ct <= 0 {
		ct = 15 * time.Second
	}
	return &Watchdog{
		cfg:           cfg,
		checks:        checks,
		oneShot:       oneShot,
		escalateAfter: ea,
		checkTimeout:  ct,
		convs:         make(map[string]*convState),
	}
}

// keyFor returns the action-identity key for dedup / justify tracking.
// Format: protocol + "|" + toolName + "|" + first-12-hex-chars-of-sha256(identity).
// For turn_end actions the identity is the reply text; for tool_call it is ToolArgs.
func keyFor(protocol string, a Action) string {
	identity := a.ToolArgs
	if a.Kind == "turn_end" {
		identity = []byte(a.Text)
	}
	sum := sha256.Sum256(identity)
	return fmt.Sprintf("%s|%s|%x", protocol, a.ToolName, sum[:6]) // 6 bytes = 12 hex chars
}

func auditID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func shortHash(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:12])
}

func summarizeActionArgs(a Action) string {
	if a.Kind == "turn_end" {
		return summarizeText(a.Text, 240)
	}
	return summarizeText(string(a.ToolArgs), 240)
}

func summarizeText(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

func (w *Watchdog) recordAudit(ctx context.Context, e AuditEvent) {
	if w.cfg.Audit == nil {
		return
	}
	if e.ID == "" {
		e.ID = auditID()
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	if err := w.cfg.Audit.Record(ctx, e); err != nil {
		log.Printf("watchdog: audit record failed: %v", err)
	}
}

// getOrCreate returns the convState for the given conversation, creating it lazily.
// Caller must hold w.mu.
func (w *Watchdog) getOrCreate(conversationID string) *convState {
	cs, ok := w.convs[conversationID]
	if !ok {
		cs = &convState{
			justified: make(map[string]bool),
			counts:    make(map[string]int),
		}
		w.convs[conversationID] = cs
	}
	return cs
}

// Gate evaluates all checks against action a and returns a Decision.
func (w *Watchdog) Gate(ctx context.Context, conversationID string, a Action) Decision {
	// Bound the whole evaluation: a supervision verdict that takes longer than
	// checkTimeout is useless, and on the turn_end path an unbounded check
	// holds the client's stream open behind it. Deadline expiry rides the
	// existing fail-open error path below.
	ctx, cancel := context.WithTimeout(ctx, w.checkTimeout)
	defer cancel()

	base := AuditEvent{
		ConversationID:  conversationID,
		ActionKind:      a.Kind,
		ToolName:        a.ToolName,
		ToolArgsHash:    shortHash(a.ToolArgs),
		ToolArgsSummary: summarizeActionArgs(a),
	}

	// Run checks; fail-open on error.
	var violation *Verdict
	for _, ch := range w.checks {
		if !ch.Applies(a) {
			continue
		}

		eval := base
		eval.EventType = "evaluation"
		eval.Check = ch.Name()
		eval.Applies = true

		var prompt, response string
		var latency time.Duration
		wrappedOneShot := func(ctx context.Context, p string) (string, error) {
			prompt = p
			start := time.Now()
			res, err := w.oneShot(ctx, p)
			latency = time.Since(start)
			response = res
			return res, err
		}

		v, err := ch.Evaluate(ctx, a, wrappedOneShot)
		eval.LatencyMS = latency.Milliseconds()
		eval.PromptHash = shortHash([]byte(prompt))
		eval.ResponseHash = shortHash([]byte(response))
		if w.cfg.AuditPrompts {
			eval.Prompt = prompt
		}
		if w.cfg.AuditResponses {
			eval.Response = response
		}
		if err != nil {
			eval.Error = err.Error()
			w.recordAudit(ctx, eval)
			log.Printf("watchdog: check %q error (fail-open): %v", ch.Name(), err)
			continue
		}

		eval.Protocol = v.Protocol
		eval.Violation = v.Violation
		eval.Challenge = v.Challenge
		eval.Revise = v.Revise
		w.recordAudit(ctx, eval)

		if v.Violation {
			vCopy := v
			violation = &vCopy
			break
		}
	}

	if violation == nil {
		w.recordAudit(ctx, AuditEvent{
			ConversationID:  conversationID,
			EventType:       "resolution",
			ActionKind:      a.Kind,
			ToolName:        a.ToolName,
			ToolArgsHash:    shortHash(a.ToolArgs),
			ToolArgsSummary: summarizeActionArgs(a),
			Decision:        "allow",
			Resolution:      "allow",
		})
		return Decision{Action: "allow"}
	}

	key := keyFor(violation.Protocol, a)

	w.mu.Lock()
	cs := w.getOrCreate(conversationID)
	if cs.justified[key] {
		w.mu.Unlock()
		w.recordAudit(ctx, AuditEvent{
			ConversationID:  conversationID,
			EventType:       "resolution",
			ActionKind:      a.Kind,
			ToolName:        a.ToolName,
			ToolArgsHash:    shortHash(a.ToolArgs),
			ToolArgsSummary: summarizeActionArgs(a),
			Protocol:        violation.Protocol,
			Violation:       true,
			Challenge:       violation.Challenge,
			Revise:          violation.Revise,
			Decision:        "allow",
			Key:             key,
			Resolution:      "allow",
			Reason:          "previously justified",
		})
		return Decision{Action: "allow", Protocol: violation.Protocol}
	}
	cs.counts[key]++
	count := cs.counts[key]
	isStrict := w.cfg.Mode == ModeStrict
	escalate := count >= w.escalateAfter
	if escalate || !isStrict {
		cs.lastChallenged = key
	}
	w.mu.Unlock()

	decision := Decision{Protocol: violation.Protocol, Challenge: violation.Challenge, Revise: violation.Revise}
	if escalate {
		decision.Action = "escalate"
	} else if isStrict {
		decision.Action = "block"
	} else {
		decision.Action = "challenge"
	}
	w.recordAudit(ctx, AuditEvent{
		ConversationID:  conversationID,
		EventType:       "resolution",
		ActionKind:      a.Kind,
		ToolName:        a.ToolName,
		ToolArgsHash:    shortHash(a.ToolArgs),
		ToolArgsSummary: summarizeActionArgs(a),
		Protocol:        violation.Protocol,
		Violation:       true,
		Challenge:       violation.Challenge,
		Revise:          violation.Revise,
		Decision:        decision.Action,
		Key:             key,
		Resolution:      decision.Action,
	})

	w.emitEcho("watchdog", fmt.Sprintf("[%s] %s: %s", decision.Action, violation.Protocol, violation.Challenge))
	return decision
}

// recordJustify marks key as justified for the given conversation so Gate
// will allow future matching actions without re-challenging.
func (w *Watchdog) recordJustify(conversationID, key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	cs := w.getOrCreate(conversationID)
	cs.justified[key] = true
}

// lastChallengedKey returns the key most recently challenged or escalated for
// the given conversation, or "" if none.
func (w *Watchdog) lastChallengedKey(conversationID string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	cs, ok := w.convs[conversationID]
	if !ok {
		return ""
	}
	return cs.lastChallenged
}
