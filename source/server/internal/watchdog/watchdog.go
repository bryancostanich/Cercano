package watchdog

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"sync"
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
}

// Config tunes Watchdog behaviour.
// EscalateAfter: number of times the same key must be seen before escalation.
// Zero means default (2).
type Config struct {
	Mode          Mode
	EscalateAfter int
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

	mu    sync.Mutex
	convs map[string]*convState

	echo func(thread, text string) // optional; nil means silent
}

// SetEcho registers a callback that is called on watchdog interventions and
// justify overrides. thread is "watchdog" or "main". Safe to call on a live
// Watchdog; replaces any previously registered callback.
func (w *Watchdog) SetEcho(fn func(thread, text string)) { w.echo = fn }

// emitEcho calls the echo callback if one is registered. Never called while
// holding w.mu — callers must release the mutex before calling emitEcho.
func (w *Watchdog) emitEcho(thread, text string) {
	if w.echo != nil {
		w.echo(thread, text)
	}
}

// New creates a ready Watchdog.
func New(cfg Config, checks []Check, oneShot OneShotFunc) *Watchdog {
	ea := cfg.EscalateAfter
	if ea <= 0 {
		ea = 2
	}
	return &Watchdog{
		cfg:           cfg,
		checks:        checks,
		oneShot:       oneShot,
		escalateAfter: ea,
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
	// Run checks; fail-open on error.
	var violation *Verdict
	for _, ch := range w.checks {
		if !ch.Applies(a) {
			continue
		}
		v, err := ch.Evaluate(ctx, a, w.oneShot)
		if err != nil {
			log.Printf("watchdog: check %q error (fail-open): %v", ch.Name(), err)
			continue
		}
		if v.Violation {
			vCopy := v
			violation = &vCopy
			break
		}
	}

	if violation == nil {
		return Decision{Action: "allow"}
	}

	key := keyFor(violation.Protocol, a)

	w.mu.Lock()
	cs := w.getOrCreate(conversationID)
	if cs.justified[key] {
		w.mu.Unlock()
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

	if escalate {
		w.emitEcho("watchdog", fmt.Sprintf("[escalate] %s: %s", violation.Protocol, violation.Challenge))
		return Decision{Action: "escalate", Protocol: violation.Protocol, Challenge: violation.Challenge}
	}
	if isStrict {
		w.emitEcho("watchdog", fmt.Sprintf("[block] %s: %s", violation.Protocol, violation.Challenge))
		return Decision{Action: "block", Protocol: violation.Protocol, Challenge: violation.Challenge}
	}
	w.emitEcho("watchdog", fmt.Sprintf("[challenge] %s: %s", violation.Protocol, violation.Challenge))
	return Decision{Action: "challenge", Protocol: violation.Protocol, Challenge: violation.Challenge}
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
