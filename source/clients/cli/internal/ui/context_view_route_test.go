package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// stubDriver is a no-op ChatDriver for tests that don't need real RPCs.
type stubDriver struct{}

func (stubDriver) Name() string { return "stub" }
func (stubDriver) Submit(_ context.Context, _ string) tea.Cmd {
	return func() tea.Msg { return chatDoneMsg{text: "ok"} }
}

func modelWithContextView() Model {
	m := Model{
		palette: theme.Cracker(),
		styles:  theme.NewStyles(theme.Cracker()),
		convID:  "c1",
	}
	m.input = newPromptInput()
	m.input.Focus()
	p, s, w, h := m.palette, m.styles, 80, 24
	d := &contextManagerDriver{agent: nil, convID: "c1"}
	cv := &contextView{
		width:   w,
		height:  h,
		palette: p,
		styles:  s,
		convID:  "c1",
	}
	cv.driver = d
	cv.chat = newChatView(s, p, "", "", w-2, h)
	m.content = cv
	return m
}

// --- new Task 4 tests ---

func TestContextView_PaneSubmitFromPromptBar(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	m.input.SetValue("drop the tangent")
	next, cmd := m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Error("enter should submit to the pane (a cmd)")
	}
	if !cv.busy() {
		t.Error("pane should be busy after submit")
	}
	if next.input.Value() != "" {
		t.Errorf("input not cleared: %q", next.input.Value())
	}
}

func TestContextView_ChatConfirmRaisesGate(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	m2, _ := m.routeChatMsg(chatConfirmMsg{
		assistant: "r",
		onYes:     func() tea.Msg { return chatDoneMsg{} },
		onNo:      func() tea.Msg { return chatDoneMsg{} },
	})
	if m2.pendingConfirm == nil {
		t.Error("chatConfirmMsg should raise the confirm gate")
	}
	// the rationale should be in the pane log
	found := false
	for _, e := range cv.chat.Entries() {
		if e.Content == "r" {
			found = true
		}
	}
	if !found {
		t.Error("assistant rationale not appended to the pane")
	}
}

func TestContextView_ChatDoneClears(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	// Submit first to get busy
	m, _ = m.submitContextEdit(cv, "hi")
	if !cv.busy() {
		t.Fatal("pane should be busy")
	}
	m.routeChatMsg(chatDoneMsg{text: "done"})
	if cv.busy() {
		t.Error("chatDoneMsg should clear busy")
	}
}

func TestContextView_UpUnstagesLastQueued(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	// Submit to make busy, then queue a second message
	m, _ = m.submitContextEdit(cv, "first")
	cv.chat.Enqueue("second", nil) // enqueued while busy
	next, _ := m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyUp})
	if next.input.Value() != "second" {
		t.Errorf("up should pop last queued into input, got %q", next.input.Value())
	}
}

func TestContextView_EscClosesPageAndClearsQueue(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	m, _ = m.submitContextEdit(cv, "first")
	cv.chat.Enqueue("queued", nil) // enqueued while busy
	// esc with empty input should close page and clear queue
	next, _ := m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyEscape})
	if next.content != nil {
		t.Error("esc with empty input should close the /c page")
	}
	if len(cv.chat.Queued()) != 0 {
		t.Error("esc should clear the pane queue")
	}
}

// --- retained behavioural tests updated for new pane+driver flow ---

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

// ProposalRaisesConfirm: chatConfirmMsg from driver raises pendingConfirm + marks turns.
func TestContextViewRoute_ProposalRaisesConfirm(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	cv.snapshot = contextSnapshot{Turns: []agentclient.ContextTurn{{ID: "a", Role: "user", Preview: "x"}}}
	// Simulate the driver emitting a chatConfirmMsg that also marks turns.
	cv.chat.Apply(chatAssistantMsg{text: "delete rationale"})
	cv.applyProposalIDs([]string{"a"})
	msg := chatConfirmMsg{
		assistant: "",
		onYes:     func() tea.Msg { return chatDoneMsg{} },
		onNo:      func() tea.Msg { return chatDoneMsg{} },
	}
	m2, _ := m.routeChatMsg(msg)
	if m2.pendingConfirm == nil {
		t.Error("chatConfirmMsg should raise a pendingConfirm")
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

// DeleteErrorSurfacesScrollback: chatErrorMsg routes to pane (shown in pane log).
func TestContextViewRoute_DeleteErrorSurfacesScrollback(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	before := len(cv.chat.Entries())
	m.routeChatMsg(chatErrorMsg{err: errors.New("rpc: unavailable")})
	if len(cv.chat.Entries()) <= before {
		t.Fatal("delete error should append an entry to the pane log")
	}
	last := cv.chat.Entries()[len(cv.chat.Entries())-1]
	if !strings.Contains(last.Content, "rpc: unavailable") {
		t.Errorf("pane error entry should contain error text, got: %q", last.Content)
	}
}

// EmptyProposalNoConfirm: chatDoneMsg (empty proposal) → no confirm gate, pane gets "nothing to remove".
func TestContextViewRoute_EmptyProposalNoConfirm(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	before := len(cv.chat.Entries())
	m2, _ := m.routeChatMsg(chatDoneMsg{text: "nothing to remove."})
	if m2.pendingConfirm != nil {
		t.Error("chatDoneMsg should not raise a pendingConfirm")
	}
	if len(cv.chat.Entries()) <= before {
		t.Fatal("chatDoneMsg should append an entry to the pane log")
	}
	last := cv.chat.Entries()[len(cv.chat.Entries())-1]
	if !strings.Contains(last.Content, "nothing to remove") {
		t.Errorf("last entry should contain 'nothing to remove', got: %q", last.Content)
	}
}
