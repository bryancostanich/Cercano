package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestSubAgentTabCreationReservesStripRows guards the layout regression where a
// sub-agent tab appearing mid-turn left bodyH/scrollbarTop stale: the strip's
// two rows overflowed the layout (status bar pushed off-screen) and the
// tab-click row was misaligned, self-correcting only when some unrelated event
// (a keystroke) forced a relayout. The fix makes refreshViewport() re-lay-out
// when the strip's shown-state drifts — so the handler path (applySubAgentEvent
// + refreshViewport) must match an explicit relayout with the tab present.
func TestSubAgentTabCreationReservesStripRows(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	if m.hasSubAgentTabs() {
		t.Fatal("precondition: no sub-agent tabs before creation")
	}

	// Exactly what the subAgentEventMsg handler does when a sub-agent tab first
	// appears: mutate the tab set, then refresh. No explicit relayout.
	m.applySubAgentEvent(subAgentEventMsg{id: "child-1", kind: "started"})
	m.refreshViewport()
	autoTop, autoH := m.scrollbarTop, m.mainChat().Height()

	// Ground truth: an explicit relayout with the tab present.
	m.relayout()
	wantTop, wantH := m.scrollbarTop, m.mainChat().Height()

	if autoTop != wantTop {
		t.Errorf("scrollbarTop after tab-create refresh = %d, want %d (strip rows not reserved without an extra relayout)", autoTop, wantTop)
	}
	if autoH != wantH {
		t.Errorf("viewport height after tab-create refresh = %d, want %d (strip rows not reserved)", autoH, wantH)
	}
	if !m.stripShown {
		t.Error("stripShown = false after the strip became visible; refreshViewport did not re-lay-out")
	}
}

// TestSubAgentTabCloseReclaimsStripRows is the inverse: closing the last
// sub-agent tab must reclaim the strip's rows on the same refresh, not leave the
// viewport two rows short.
func TestSubAgentTabCloseReclaimsStripRows(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.applySubAgentEvent(subAgentEventMsg{id: "child-1", kind: "started"})
	m.refreshViewport()
	if !m.hasSubAgentTabs() || !m.stripShown {
		t.Fatal("precondition: strip visible after creating a sub-agent tab")
	}

	// Close the only sub-agent tab and refresh, as the close paths do.
	m.closeSubAgentTab("child-1")
	m.refreshViewport()
	autoTop, autoH := m.scrollbarTop, m.mainChat().Height()

	m.relayout()
	wantTop, wantH := m.scrollbarTop, m.mainChat().Height()

	if autoTop != wantTop || autoH != wantH {
		t.Errorf("after closing last sub tab: got top=%d h=%d, want top=%d h=%d (strip rows not reclaimed)", autoTop, autoH, wantTop, wantH)
	}
	if m.stripShown {
		t.Error("stripShown = true after the last sub-agent tab was closed")
	}
}
