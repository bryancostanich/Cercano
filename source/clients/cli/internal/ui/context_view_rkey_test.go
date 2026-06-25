package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// In /c the prompt bar is the input, so a bare "r" must type, not trigger a
// reload (the old read-only 3a hotkey). Refresh moved to ctrl+r.
func TestContextViewRoute_RTypesIntoPromptBar(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	next, _ := m.handleContextViewKey(cv, tea.KeyPressMsg{Code: 'r', Text: "r"})
	if next.input.Value() != "r" {
		t.Errorf("'r' should type into the prompt bar, got %q", next.input.Value())
	}
}

func TestContextViewRoute_CtrlRDoesNotType(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	next, _ := m.handleContextViewKey(cv, tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if next.input.Value() != "" {
		t.Errorf("ctrl+r should refresh, not type; input = %q", next.input.Value())
	}
}
