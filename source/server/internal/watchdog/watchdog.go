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
// Format: protocol + "|" + toolName + "|" + first-12-hex-chars-of-sha256(toolArgs).
func keyFor(protocol string, a Action) string {
	sum := sha256.Sum256(a.ToolArgs)
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
		return Decision{Action: "escalate", Protocol: violation.Protocol, Challenge: violation.Challenge}
	}
	if isStrict {
		return Decision{Action: "block", Protocol: violation.Protocol, Challenge: violation.Challenge}
	}
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
