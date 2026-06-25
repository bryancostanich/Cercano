package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/server/pkg/agentclient"
)

// In /c, with an empty prompt, up/down arrows MOVE the section focus (they do
// NOT expand); right expands the focused section, left collapses it (tab/enter
// still work too). With a non-empty prompt, arrows drive the textarea cursor.
func twoExpandableTurns(cv *contextView) {
	cv.snapshot.Turns = []agentclient.ContextTurn{
		{ID: "t1", Role: "user", Truncated: true},
		{ID: "t2", Role: "assistant", Truncated: true},
	}
	cv.focusedTurn = -1
	cv.expanded = map[string]bool{}
}

func TestContextView_DownArrowFocusesNoExpand(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	twoExpandableTurns(cv)
	next, _ := m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyDown})
	if cv.focusedTurn != 0 {
		t.Fatalf("down should focus the first expandable turn, got %d", cv.focusedTurn)
	}
	if cv.expanded["t1"] {
		t.Errorf("down must NOT auto-expand the focused turn")
	}
	if next.input.Value() != "" {
		t.Errorf("down (empty prompt) must not type into the prompt, got %q", next.input.Value())
	}
	m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyDown})
	if cv.focusedTurn != 1 || cv.expanded["t2"] {
		t.Errorf("second down should focus t2 without expanding; focused=%d expanded[t2]=%v", cv.focusedTurn, cv.expanded["t2"])
	}
}

func TestContextView_UpArrowFocusesNoExpand(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	twoExpandableTurns(cv)
	m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyUp})
	if cv.focusedTurn != 1 {
		t.Errorf("up should focus the last expandable turn, got %d", cv.focusedTurn)
	}
	if cv.expanded["t2"] {
		t.Errorf("up must NOT auto-expand")
	}
}

func TestContextView_RightExpandsLeftCollapses(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	twoExpandableTurns(cv)
	m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyDown}) // focus t1
	m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyRight})
	if !cv.expanded["t1"] {
		t.Errorf("right should expand the focused section")
	}
	m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyLeft})
	if cv.expanded["t1"] {
		t.Errorf("left should collapse the focused section")
	}
}

func TestContextView_ArrowsTypeWhenPromptNonEmpty(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	twoExpandableTurns(cv)
	m.input.SetValue("hello")
	m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyDown})
	m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyRight})
	if cv.focusedTurn != -1 || cv.expanded["t1"] {
		t.Errorf("with a non-empty prompt, arrows must not navigate/expand sections; focused=%d expanded[t1]=%v", cv.focusedTurn, cv.expanded["t1"])
	}
}
