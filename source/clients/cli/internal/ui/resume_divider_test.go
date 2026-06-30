package ui

import (
	"strings"
	"testing"
	"time"

	"cercano/source/server/pkg/agentclient"
)

// resumeEntries with no compaction (frozenThrough == 0) must not insert
// a divider at all — the model has the full verbatim history.
func TestResumeEntries_NoDividerWhenNoCompaction(t *testing.T) {
	turns := []agentclient.PersistedTurn{
		{Role: "user", Content: "hi", CreatedAt: time.Unix(100, 0)},
		{Role: "assistant", Content: "hello", CreatedAt: time.Unix(101, 0)},
	}
	got := resumeEntries(turns, 0)
	for _, e := range got {
		if e.Role == RoleDivider {
			t.Errorf("expected no divider when frozenThrough=0, got: %+v", e)
		}
	}
}

// Divider must appear between the last frozen turn and the first live turn.
// Frozen turns have CreatedAt.Unix() <= frozenThrough.
func TestResumeEntries_DividerAtFreezeBoundary(t *testing.T) {
	turns := []agentclient.PersistedTurn{
		{Role: "user", Content: "first", CreatedAt: time.Unix(100, 0)},     // frozen
		{Role: "assistant", Content: "second", CreatedAt: time.Unix(150, 0)}, // frozen
		{Role: "user", Content: "third", CreatedAt: time.Unix(200, 0)},     // live (first post-freeze)
		{Role: "assistant", Content: "fourth", CreatedAt: time.Unix(210, 0)}, // live
	}
	got := resumeEntries(turns, 150)
	// Expected: first, second, [divider], third, fourth — 5 entries.
	if len(got) != 5 {
		t.Fatalf("expected 5 entries (4 turns + divider), got %d", len(got))
	}
	if got[0].Content != "first" || got[1].Content != "second" {
		t.Errorf("frozen turns should appear first; got %q, %q", got[0].Content, got[1].Content)
	}
	if got[2].Role != RoleDivider {
		t.Errorf("entry 2 should be the divider, got role=%v content=%q", got[2].Role, got[2].Content)
	}
	if !strings.Contains(got[2].Content, "2 turn(s) compacted") {
		t.Errorf("divider should mention 2 frozen turns, got: %q", got[2].Content)
	}
	if got[3].Content != "third" || got[4].Content != "fourth" {
		t.Errorf("live turns should follow divider; got %q, %q", got[3].Content, got[4].Content)
	}
}

// All turns are frozen (no live tail) — divider goes at the end so the
// user's next prompt clearly lands below the freeze line.
func TestResumeEntries_DividerAtEndWhenAllFrozen(t *testing.T) {
	turns := []agentclient.PersistedTurn{
		{Role: "user", Content: "a", CreatedAt: time.Unix(100, 0)},
		{Role: "assistant", Content: "b", CreatedAt: time.Unix(110, 0)},
	}
	got := resumeEntries(turns, 200) // all turns frozen
	if len(got) != 3 {
		t.Fatalf("expected 3 entries (2 turns + trailing divider), got %d", len(got))
	}
	if got[2].Role != RoleDivider {
		t.Errorf("entry 2 (last) should be the divider when all frozen, got role=%v", got[2].Role)
	}
}

// No turns are frozen (frozenThrough set but earlier than any turn) — the
// divider should appear at the very top, before the first turn.
func TestResumeEntries_DividerAtTopWhenNoneFrozen(t *testing.T) {
	turns := []agentclient.PersistedTurn{
		{Role: "user", Content: "a", CreatedAt: time.Unix(100, 0)},
		{Role: "assistant", Content: "b", CreatedAt: time.Unix(110, 0)},
	}
	got := resumeEntries(turns, 50) // no turns frozen, but frozenThrough non-zero
	if len(got) != 3 {
		t.Fatalf("expected 3 entries (divider + 2 turns), got %d", len(got))
	}
	if got[0].Role != RoleDivider {
		t.Errorf("entry 0 should be divider when none frozen, got role=%v", got[0].Role)
	}
	if !strings.Contains(got[0].Content, "0 turn(s) compacted") {
		t.Errorf("divider should reflect 0 frozen turns, got: %q", got[0].Content)
	}
}

// Empty-content turns (tool_use / tool_result skipped on resume) must not
// break boundary detection — the divider still goes at the right place
// relative to the kept turns.
func TestResumeEntries_BoundaryRespectsSkippedEmptyTurns(t *testing.T) {
	turns := []agentclient.PersistedTurn{
		{Role: "user", Content: "first", CreatedAt: time.Unix(100, 0)},  // frozen, kept
		{Role: "assistant", Content: "", CreatedAt: time.Unix(150, 0)},  // frozen, skipped (empty)
		{Role: "user", Content: "third", CreatedAt: time.Unix(200, 0)},  // live, kept
	}
	got := resumeEntries(turns, 150)
	// Expected: first, [divider], third — 3 entries.
	if len(got) != 3 {
		t.Fatalf("expected 3 entries (2 kept turns + divider), got %d", len(got))
	}
	if got[1].Role != RoleDivider {
		t.Errorf("entry 1 should be the divider, got role=%v", got[1].Role)
	}
}
