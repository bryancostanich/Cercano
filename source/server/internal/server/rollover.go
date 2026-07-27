package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/conversation"
)

// rolloverConfig are the knobs that arm and shape agent-offered session
// rollover. Zero RawTokenThreshold AND zero ReconsolidationThreshold means the
// feature is fully off: newRolloverManager returns a manager whose ShouldOffer
// always reports false, so existing behavior is byte-identical.
type rolloverConfig struct {
	RawTokenThreshold        int64   // cumulative raw tokens that arms an offer; 0 disables the token trigger
	ReconsolidationThreshold int     // OR-trigger on re-consolidation count; 0 ignores it
	RearmMultiple            float64 // growth multiple past a decline before re-offering; <=1 => default 1.5
	VerbatimTurns            int     // turns kept verbatim in the handoff; <=0 => default 6
}

const (
	defaultRearmMultiple  = 1.5
	defaultVerbatimTurns  = 6
	rolloverOfferIDBytes  = 8 // 16 hex chars — enough to correlate a reply, cheap to compare
)

// convOfferState is the per-conversation hysteresis record. A conversation is
// in exactly one of: never offered (no entry), offered-and-awaiting-reply,
// declined-and-disarmed (rearmAt set), or accepted-and-done (done=true).
type convOfferState struct {
	lastOfferID string // the offer we're currently awaiting a reply for ("" once resolved)
	rearmAt     int64  // raw-token level at/above which we may offer again (0 = armed now)
	done        bool   // Accept was received — never offer this conversation again
}

// rolloverManager decides when to offer a session rollover and enforces
// hysteresis so a user isn't nagged. In-process, keyed by conversation id, safe
// for concurrent turns via mu. Pure of I/O — callers supply the raw-token count
// and reconsolidation count; the manager only tracks offer state.
type rolloverManager struct {
	cfg rolloverConfig
	mu  sync.Mutex
	st  map[string]*convOfferState
}

func newRolloverManager(cfg rolloverConfig) *rolloverManager {
	if cfg.RearmMultiple <= 1 {
		cfg.RearmMultiple = defaultRearmMultiple
	}
	if cfg.VerbatimTurns <= 0 {
		cfg.VerbatimTurns = defaultVerbatimTurns
	}
	return &rolloverManager{cfg: cfg, st: map[string]*convOfferState{}}
}

// enabled reports whether any trigger is armed. When false the manager is inert.
func (m *rolloverManager) enabled() bool {
	return m.cfg.RawTokenThreshold > 0 || m.cfg.ReconsolidationThreshold > 0
}

// ShouldOffer reports whether to emit a RolloverOffered for this conversation
// right now, given the current cumulative raw tokens and reconsolidation count.
// It does NOT mutate state — the caller commits with NoteOffered only after the
// event is actually sent, so a send failure doesn't disarm the offer. Returns a
// human-readable reason for the offer prompt.
func (m *rolloverManager) ShouldOffer(convID string, rawTokens int64, reconsolidations int) (bool, string) {
	if m == nil || !m.enabled() {
		return false, ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.st[convID]
	if s != nil {
		if s.done {
			return false, "" // accepted — this conversation has rolled over
		}
		if s.lastOfferID != "" {
			return false, "" // an offer is already outstanding; await the reply
		}
		if s.rearmAt > 0 && rawTokens < s.rearmAt {
			return false, "" // declined and not yet grown past the re-arm line
		}
	}
	tokenTrip := m.cfg.RawTokenThreshold > 0 && rawTokens >= m.cfg.RawTokenThreshold
	reconTrip := m.cfg.ReconsolidationThreshold > 0 && reconsolidations >= m.cfg.ReconsolidationThreshold
	switch {
	case tokenTrip:
		return true, fmt.Sprintf("this session has grown to ~%d tokens of raw context", rawTokens)
	case reconTrip:
		return true, fmt.Sprintf("this session's context has been re-consolidated %d times", reconsolidations)
	default:
		return false, ""
	}
}

// NoteOffered records that an offer was emitted, so ShouldOffer won't re-offer
// until the reply arrives. Returns the generated offer id to stamp on the event.
func (m *rolloverManager) NoteOffered(convID string, rawTokens int64) string {
	id := newOfferID()
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.st[convID]
	if s == nil {
		s = &convOfferState{}
		m.st[convID] = s
	}
	s.lastOfferID = id
	return id
}

// NoteDeclined clears the outstanding offer and disarms until raw tokens grow
// past RearmMultiple × the decline level (hysteresis). A stale/mismatched
// offerID is ignored (returns false) so a late reply to a superseded offer
// can't move state.
func (m *rolloverManager) NoteDeclined(convID, offerID string, rawTokens int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.st[convID]
	if s == nil || s.lastOfferID == "" || s.lastOfferID != offerID {
		return false
	}
	s.lastOfferID = ""
	rearm := int64(float64(rawTokens) * m.cfg.RearmMultiple)
	if rearm <= rawTokens {
		rearm = rawTokens + 1 // guarantee forward progress even at tiny token counts
	}
	s.rearmAt = rearm
	return true
}

// NoteAccepted marks the conversation as rolled over — no further offers. A
// stale/mismatched offerID is ignored.
func (m *rolloverManager) NoteAccepted(convID, offerID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.st[convID]
	if s == nil || s.lastOfferID == "" || s.lastOfferID != offerID {
		return false
	}
	s.lastOfferID = ""
	s.done = true
	return true
}

// verbatimTurns exposes the configured verbatim-tail size for the caller that
// builds the handoff.
func (m *rolloverManager) verbatimTurns() int {
	if m == nil {
		return defaultVerbatimTurns
	}
	return m.cfg.VerbatimTurns
}

func newOfferID() string {
	var b [rolloverOfferIDBytes]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// buildHandoff renders the durable artifact that seeds a rolled-over session:
// the structured summary (verbatim, via RenderBlock) followed by a delimited
// tail of the last min(verbatimN, len) turns as "role: content". This same
// string is shown as the offer's handoff_preview AND stored as the new
// conversation's first turn, so what the user sees is exactly what warms the new
// session. Pure and deterministic. verbatimN <= 0 falls back to the default.
func buildHandoff(summary compaction.StructuredSummary, recentTurns []conversation.Turn, verbatimN int) string {
	if verbatimN <= 0 {
		verbatimN = defaultVerbatimTurns
	}
	var b strings.Builder
	b.WriteString(summary.RenderBlock().Text)

	tail := recentTurns
	if len(tail) > verbatimN {
		tail = tail[len(tail)-verbatimN:]
	}
	if len(tail) > 0 {
		b.WriteString("\n\n--- recent turns ---\n")
		for i, t := range tail {
			if i > 0 {
				b.WriteString("\n")
			}
			role := t.Role
			if role == "" {
				role = "user"
			}
			fmt.Fprintf(&b, "%s: %s", role, t.Content)
		}
	}
	return b.String()
}
