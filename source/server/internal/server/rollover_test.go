package server

import (
	"strings"
	"testing"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/conversation"
)

func turnsN(n int) []conversation.Turn {
	out := make([]conversation.Turn, n)
	for i := range out {
		out[i] = conversation.Turn{Role: "user", Content: itoaTurn(i)}
	}
	return out
}

func itoaTurn(i int) string { return "turn-" + string(rune('A'+i)) }

// buildHandoff: preview leads with the summary, includes the last N turns (not
// older ones), clamps to available turns, and is deterministic.
func TestBuildHandoff_SummaryPlusTail(t *testing.T) {
	sum := compaction.StructuredSummary{Goal: "ship the widget", State: "compiling"}
	turns := turnsN(10) // A..J
	got := buildHandoff(sum, turns, 3)

	if !strings.Contains(got, "ship the widget") {
		t.Errorf("handoff should contain the summary Goal, got:\n%s", got)
	}
	// Last 3 (H,I,J) present; an older one (A) absent from the tail section.
	for _, want := range []string{"turn-H", "turn-I", "turn-J"} {
		if !strings.Contains(got, want) {
			t.Errorf("handoff tail should include %s, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "turn-A") {
		t.Errorf("handoff tail should NOT include the oldest turn, got:\n%s", got)
	}
	// Deterministic.
	if buildHandoff(sum, turns, 3) != got {
		t.Error("buildHandoff is not deterministic")
	}
}

func TestBuildHandoff_ClampsAndDefaults(t *testing.T) {
	sum := compaction.StructuredSummary{Goal: "g"}
	// Fewer turns than verbatimN: all included, no panic.
	got := buildHandoff(sum, turnsN(2), 6)
	if !strings.Contains(got, "turn-A") || !strings.Contains(got, "turn-B") {
		t.Errorf("all turns should be included when fewer than N, got:\n%s", got)
	}
	// verbatimN<=0 falls back to default (6) rather than dropping the tail.
	got2 := buildHandoff(sum, turnsN(2), 0)
	if !strings.Contains(got2, "recent turns") {
		t.Errorf("verbatimN<=0 should default, not drop the tail, got:\n%s", got2)
	}
	// No turns: just the summary, no tail header.
	got3 := buildHandoff(sum, nil, 6)
	if strings.Contains(got3, "recent turns") {
		t.Errorf("no turns should mean no tail section, got:\n%s", got3)
	}
}

// Below threshold => no offer; crossing it => offer exactly once (state armed to
// the outstanding offer).
func TestRolloverManager_OffersOnceAtThreshold(t *testing.T) {
	m := newRolloverManager(rolloverConfig{RawTokenThreshold: 1000})

	if offer, _ := m.ShouldOffer("c1", 999, 0); offer {
		t.Fatal("should not offer below threshold")
	}
	offer, reason := m.ShouldOffer("c1", 1000, 0)
	if !offer {
		t.Fatal("should offer at threshold")
	}
	if reason == "" {
		t.Error("offer should carry a reason")
	}
	// Commit the offer, then a second check at the same tokens must NOT re-offer.
	m.NoteOffered("c1", 1000)
	if offer, _ := m.ShouldOffer("c1", 1200, 0); offer {
		t.Fatal("should not re-offer while an offer is outstanding")
	}
}

// Decline disarms until growth passes rearm*level; then it offers again.
func TestRolloverManager_DeclineRearms(t *testing.T) {
	m := newRolloverManager(rolloverConfig{RawTokenThreshold: 1000, RearmMultiple: 1.5})
	m.ShouldOffer("c1", 1000, 0)
	id := m.NoteOffered("c1", 1000)

	if !m.NoteDeclined("c1", id, 1000) {
		t.Fatal("decline with the right offer id should succeed")
	}
	// Re-arm line is 1000*1.5 = 1500. Below it: no offer.
	if offer, _ := m.ShouldOffer("c1", 1499, 0); offer {
		t.Fatal("should stay disarmed below the re-arm line")
	}
	// At/above it: offers again.
	if offer, _ := m.ShouldOffer("c1", 1500, 0); !offer {
		t.Fatal("should re-offer once grown past the re-arm line")
	}
}

// Accept ends offering permanently for that conversation.
func TestRolloverManager_AcceptIsTerminal(t *testing.T) {
	m := newRolloverManager(rolloverConfig{RawTokenThreshold: 1000})
	m.ShouldOffer("c1", 1000, 0)
	id := m.NoteOffered("c1", 1000)
	if !m.NoteAccepted("c1", id) {
		t.Fatal("accept with the right id should succeed")
	}
	if offer, _ := m.ShouldOffer("c1", 100000, 0); offer {
		t.Fatal("an accepted conversation must never be offered again")
	}
}

// A stale/mismatched offer id is rejected on both Accept and Decline.
func TestRolloverManager_StaleOfferRejected(t *testing.T) {
	m := newRolloverManager(rolloverConfig{RawTokenThreshold: 1000})
	m.ShouldOffer("c1", 1000, 0)
	m.NoteOffered("c1", 1000)
	if m.NoteAccepted("c1", "not-the-id") {
		t.Error("accept must reject a mismatched offer id")
	}
	if m.NoteDeclined("c1", "not-the-id", 1000) {
		t.Error("decline must reject a mismatched offer id")
	}
}

// The reconsolidation OR-trigger fires independently of the token trigger.
func TestRolloverManager_ReconsolidationTrigger(t *testing.T) {
	m := newRolloverManager(rolloverConfig{ReconsolidationThreshold: 3})
	if offer, _ := m.ShouldOffer("c1", 0, 2); offer {
		t.Fatal("should not offer below the reconsolidation threshold")
	}
	if offer, _ := m.ShouldOffer("c1", 0, 3); !offer {
		t.Fatal("should offer at the reconsolidation threshold")
	}
}

// Zero thresholds => feature off => never offers, regardless of size.
func TestRolloverManager_DisabledByDefault(t *testing.T) {
	m := newRolloverManager(rolloverConfig{})
	if m.enabled() {
		t.Fatal("zero-threshold manager should report disabled")
	}
	if offer, _ := m.ShouldOffer("c1", 1<<30, 100); offer {
		t.Fatal("a disabled manager must never offer")
	}
}
