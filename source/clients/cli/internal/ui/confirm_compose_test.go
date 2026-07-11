package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestToolConfirm_ChatEntersComposeMode covers the CLI half of "[c] chat about
// this": a stream-origin tool confirm offers [c], which dismisses the confirm
// and drops into the compose sub-state; a local /tool confirm does not (there is
// no server tool loop to redirect); and the hint advertises the affordance.
func TestToolConfirm_ChatEntersComposeMode(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Stream-origin call: [c] is wired and enters compose mode.
	cr := toolConfirm(&pendingToolCall{ToolUseID: "u1", Name: "Write", Args: "{}"})
	fn, ok := cr.extras["c"]
	if !ok {
		t.Fatal("[c] not wired for a stream-origin tool call")
	}
	m.pendingConfirm = cr
	m2, _ := fn(m)
	if m2.composeToolUseID != "u1" {
		t.Errorf("composeToolUseID = %q, want %q", m2.composeToolUseID, "u1")
	}
	if m2.pendingConfirm != nil {
		t.Error("pendingConfirm should be cleared when entering compose mode")
	}

	// Local /tool origin (no ToolUseID): no [c] — nothing on the server to redirect.
	if _, ok := toolConfirm(&pendingToolCall{Name: "localcmd"}).extras["c"]; ok {
		t.Error("[c] should not be offered for a local /tool call")
	}

	// The confirm hint advertises [c]hat for stream-origin calls but not local ones.
	if h := m.confirmPromptHints(&pendingToolCall{ToolUseID: "u1", Name: "Write"}); !strings.Contains(h, "hat") {
		t.Errorf("stream-origin confirm hint missing [c]hat: %q", h)
	}
	if h := m.confirmPromptHints(&pendingToolCall{Name: "localcmd"}); strings.Contains(h, "hat") {
		t.Errorf("local confirm hint should not advertise [c]hat: %q", h)
	}
}
