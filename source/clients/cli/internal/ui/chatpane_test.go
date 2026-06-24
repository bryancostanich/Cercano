package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"cercano/source/clients/cli/internal/theme"
)

// fakeDriver emits a scripted single status then an assistant message via cmds.
type fakeDriver struct{ name string }

func (d fakeDriver) Name() string { return d.name }
func (d fakeDriver) Submit(_ context.Context, input string) tea.Cmd {
	return func() tea.Msg { return chatAssistantMsg{text: "echo: " + input} }
}

func newTestPane() *chatPane {
	return newChatPane(fakeDriver{name: "tester"}, theme.NewStyles(theme.Cracker()), theme.Cracker(), 80, 12)
}

func TestChatPane_SubmitAppendsUserAndBusy(t *testing.T) {
	p := newTestPane()
	cmd := p.Submit("hello")
	if cmd == nil {
		t.Error("Submit should return the driver cmd")
	}
	if !p.Busy() {
		t.Error("pane should be busy after Submit")
	}
	if len(p.entries) != 1 || p.entries[0].Role != RoleUser || p.entries[0].Content != "hello" {
		t.Errorf("expected one user entry, got %+v", p.entries)
	}
}

func TestChatPane_ApplyAssistantAndDone(t *testing.T) {
	p := newTestPane()
	p.Submit("hi")
	p.Apply(chatAssistantMsg{text: "echo: hi"})
	p.Apply(chatDoneMsg{})
	if p.Busy() {
		t.Error("done should clear busy")
	}
	out := p.View()
	if !strings.Contains(out, "echo: hi") {
		t.Errorf("assistant text missing:\n%s", out)
	}
}

func TestChatPane_StatusShownWhileBusy(t *testing.T) {
	p := newTestPane()
	p.Submit("x")
	p.Apply(chatStatusMsg{activity: "thinking…"})
	if !strings.Contains(p.View(), "thinking…") {
		t.Error("busy status not rendered")
	}
}

func TestChatPane_ErrorClearsBusyAndShows(t *testing.T) {
	p := newTestPane()
	p.Submit("x")
	p.Apply(chatErrorMsg{err: errString("boom")})
	if p.Busy() {
		t.Error("error should clear busy")
	}
	if !strings.Contains(p.View(), "boom") {
		t.Error("error text not shown")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestChatPane_QueuesWhileBusyAndDrains(t *testing.T) {
	p := newTestPane()
	p.Submit("first") // starts; busy
	if p.Submit("second") != nil { // busy → enqueue, returns nil
		t.Error("submit while busy should enqueue (nil cmd), not start")
	}
	if len(p.queued) != 1 || p.queued[0] != "second" {
		t.Fatalf("queue = %v, want [second]", p.queued)
	}
	if !strings.Contains(p.View(), "second") {
		t.Error("queued message should render")
	}
	// ending the first exchange auto-drains "second" (returns its start cmd)
	cmd := p.Apply(chatDoneMsg{})
	if cmd == nil {
		t.Error("done with a queued msg should return the drain cmd")
	}
	if len(p.queued) != 0 {
		t.Errorf("queue should be empty after drain, got %v", p.queued)
	}
	if !p.Busy() {
		t.Error("draining the next message should make the pane busy again")
	}
	// last queued can be unstaged back to the caller
	p.Submit("third")
	msg, ok := p.unstageLastQueued()
	if !ok || msg != "third" {
		t.Errorf("unstageLastQueued = (%q, %v), want (third, true)", msg, ok)
	}
	if len(p.queued) != 0 {
		t.Errorf("queue should be empty after unstage, got %v", p.queued)
	}
}
