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

	h0 := m.viewport.Height()

	next, _ := m.Update(recapLoadedMsg{recap: "wired the engine badge"})
	m = next.(Model)
	if got := m.viewport.Height(); got != h0-1 {
		t.Errorf("recap appearing should shrink viewport by 1 (reserve its row): %d -> %d, want %d", h0, got, h0-1)
	}

	// Clearing it frees the row again.
	next, _ = m.Update(recapLoadedMsg{recap: ""})
	m = next.(Model)
	if got := m.viewport.Height(); got != h0 {
		t.Errorf("recap clearing should restore viewport height: want %d, got %d", h0, got)
	}
}
