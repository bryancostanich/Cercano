package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func modelWithContextView() Model {
	m := Model{
		palette:      theme.Cracker(),
		styles:       theme.NewStyles(theme.Cracker()),
		convID:       "c1",
		focusedToolIdx: -1,
	}
	m.input = newPromptInput()
	m.input.Focus()
	cv := &contextView{width: 80, height: 24, palette: m.palette, styles: m.styles, convID: "c1"}
	m.content = cv
	return m
}

func TestContextViewRoute_TypingEditsPromptBar(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	next, _ := m.handleContextViewKey(cv, tea.KeyPressMsg{Code: 'h', Text: "h"})
	if next.input.Value() != "h" {
		t.Errorf("typing did not reach the prompt bar: %q", next.input.Value())
	}
}

func TestContextViewRoute_EnterProposes(t *testing.T) {
	m := modelWithContextView()
	m.input.SetValue("drop the tangent")
	cv := m.content.(*contextView)
	next, cmd := m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Error("enter with text should return a propose cmd")
	}
	if next.input.Value() != "" {
		t.Errorf("input not cleared after submit: %q", next.input.Value())
	}
}

func TestContextViewRoute_ProposalRaisesConfirm(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	cv.snapshot = contextSnapshot{Turns: []agentclient.ContextTurn{{ID: "a", Role: "user", Preview: "x"}}}
	m2, _ := m.onContextProposal(contextEditProposalMsg{p: agentclient.Proposal{DeleteIDs: []string{"a"}, Rationale: "r"}})
	if m2.pendingConfirm == nil {
		t.Error("proposal should raise a pendingConfirm")
	}
	if !cv.markedForDelete("a") {
		t.Error("proposal should mark turn a")
	}
}

func TestContextViewRoute_EscWithTextClearsInput(t *testing.T) {
	m := modelWithContextView()
	m.input.SetValue("partial text")
	cv := m.content.(*contextView)
	next, _ := m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyEscape})
	if next.input.Value() != "" {
		t.Errorf("esc with non-empty input should clear it, got %q", next.input.Value())
	}
	if next.content == nil {
		t.Error("esc with non-empty input should not close the page")
	}
}

func TestContextViewRoute_EscWithEmptyInputClosesPage(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	next, _ := m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyEscape})
	if next.content != nil {
		t.Error("esc with empty input should close the /c page")
	}
}

func TestContextViewRoute_EnterWithEmptyInputIsNoop(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	next, cmd := m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Error("enter with empty input should not fire a cmd")
	}
	if next.content == nil {
		t.Error("enter with empty input should not close the page")
	}
}

func TestContextViewRoute_DeleteErrorSurfacesScrollback(t *testing.T) {
	m := modelWithContextView()
	before := len(m.entries)
	m2, _ := m.onContextDeleted(contextEditDeletedMsg{err: errors.New("rpc: unavailable")})
	if len(m2.entries) <= before {
		t.Fatal("delete error should append a scrollback entry")
	}
	last := m2.entries[len(m2.entries)-1]
	if !strings.Contains(last.Content, "couldn't delete") {
		t.Errorf("last entry should contain 'couldn't delete', got: %q", last.Content)
	}
	// snapshot should NOT be reloaded on error — cv.snapshot stays zero
	cv := m2.content.(*contextView)
	if cv.snapshot.Turns != nil {
		t.Error("snapshot should not be reloaded on delete error")
	}
}

func TestContextViewRoute_EmptyProposalNoConfirm(t *testing.T) {
	m := modelWithContextView()
	before := len(m.entries)
	m2, _ := m.onContextProposal(contextEditProposalMsg{p: agentclient.Proposal{DeleteIDs: []string{}, Rationale: "r"}})
	if m2.pendingConfirm != nil {
		t.Error("empty proposal should not raise a pendingConfirm")
	}
	if len(m2.entries) <= before {
		t.Fatal("empty proposal should append a scrollback entry")
	}
	last := m2.entries[len(m2.entries)-1]
	if !strings.Contains(last.Content, "nothing to remove") {
		t.Errorf("last entry should contain 'nothing to remove', got: %q", last.Content)
	}
}
