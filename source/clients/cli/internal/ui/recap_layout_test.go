package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// When a living-recap appears, View renders an extra line below the viewport.
// relayout must shrink the viewport by that row so the status bar stays pinned —
// otherwise the recap line pushes the footer off-screen. The recapLoadedMsg
// handler must therefore re-run relayout when the recap's presence toggles.
func TestRecapAppearanceReservesViewportRow(t *testing.T) {
	m := New(nil, false)
	m = m.SeedAssistantMarkdown("some prior reply\n")
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	h0 := m.mainChat().Height()

	next, _ := m.Update(recapLoadedMsg{recap: "wired the engine badge"})
	m = next.(Model)
	if got := m.mainChat().Height(); got != h0-2 {
		t.Errorf("recap appearing should shrink viewport by 2 (blank spacer + recap line): %d -> %d, want %d", h0, got, h0-2)
	}

	// Clearing it frees the row again.
	next, _ = m.Update(recapLoadedMsg{recap: ""})
	m = next.(Model)
	if got := m.mainChat().Height(); got != h0 {
		t.Errorf("recap clearing should restore viewport height: want %d, got %d", h0, got)
	}
}

// TestRecapWrapsAndReservesAllRows verifies a long recap wraps to multiple lines
// and the viewport shrinks by the blank spacer + every wrapped line (not a fixed
// 2), so the status bar stays pinned regardless of recap length.
func TestRecapWrapsAndReservesAllRows(t *testing.T) {
	m := New(nil, false)
	m = m.SeedAssistantMarkdown("some prior reply\n")
	m = send(t, m, tea.WindowSizeMsg{Width: 60, Height: 30})

	h0 := m.mainChat().Height()

	long := "refactored the telemetry collector to guard Emit against a closed channel, added a race regression test, and rebuilt the server"
	next, _ := m.Update(recapLoadedMsg{recap: long})
	m = next.(Model)

	n := len(m.recapLines())
	if n < 2 {
		t.Fatalf("test needs a recap that wraps to >1 line at width 60; got %d", n)
	}
	if got, want := m.mainChat().Height(), h0-(1+n); got != want {
		t.Errorf("wrapped recap (%d lines) should shrink viewport by 1+%d: %d -> %d, want %d", n, n, h0, got, want)
	}
}

// TestRecapGrowthRelayoutsWithoutPresenceToggle guards the reported regression:
// the living recap updates its text mid-session (no presence toggle), growing
// from a single wrapped row to several. The old handler relayouted only when the
// recap's presence flipped, so a text-only growth left the viewport a row too
// tall and shoved the status bar (bottom toolbar) off-screen until the next
// keystroke forced a relayout. The handler must relayout on any line-count
// change, so the viewport tracks the recap's height while it's already present.
func TestRecapGrowthRelayoutsWithoutPresenceToggle(t *testing.T) {
	m := New(nil, false)
	m = m.SeedAssistantMarkdown("some prior reply\n")
	m = send(t, m, tea.WindowSizeMsg{Width: 60, Height: 30})

	// Start with a short recap that wraps to exactly one line.
	next, _ := m.Update(recapLoadedMsg{recap: "wired the badge"})
	m = next.(Model)
	if n := len(m.recapLines()); n != 1 {
		t.Fatalf("setup: short recap should be 1 line at width 60, got %d", n)
	}
	shortH := m.mainChat().Height()

	// Grow the recap text — no presence toggle, it was already non-empty — into
	// something that wraps to multiple rows.
	long := "refactored the telemetry collector to guard Emit against a closed channel, added a race regression test, and rebuilt the server"
	next, _ = m.Update(recapLoadedMsg{recap: long})
	m = next.(Model)

	n := len(m.recapLines())
	if n < 2 {
		t.Fatalf("test needs a recap that wraps to >1 line at width 60; got %d", n)
	}
	// The viewport must shrink by the extra rows the longer recap now claims.
	if got, want := m.mainChat().Height(), shortH-(n-1); got != want {
		t.Errorf("recap growing 1 -> %d lines should shrink viewport by %d rows: %d -> %d, want %d",
			n, n-1, shortH, got, want)
	}
}
