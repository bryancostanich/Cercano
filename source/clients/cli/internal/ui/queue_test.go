package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Submitting a message while a response is streaming queues it instead of
// starting another turn.
func TestSubmitWhileStreamingQueues(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true // pretend a response is in flight
	m.input.SetValue("check the tests")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)

	if q := m.mainChat().Queued(); len(q) != 1 || q[0] != "check the tests" {
		t.Fatalf("expected queued [check the tests], got %v", q)
	}
	if strings.TrimSpace(m.input.Value()) != "" {
		t.Errorf("input should clear after queuing, got %q", m.input.Value())
	}
	if !m.streaming {
		t.Errorf("queuing must not end the in-flight stream")
	}
}

// Up-arrow on an empty prompt pulls the most-recently-queued message back into
// the prompt and removes it from the queue.
func TestUpArrowUnstagesLastQueued(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.mainChat().Enqueue("check the tests", nil)
	m.mainChat().Enqueue("run the linter", nil)
	m.input.SetValue("")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = next.(Model)

	if m.input.Value() != "run the linter" {
		t.Errorf("up-arrow should unstage the last queued message, got input %q", m.input.Value())
	}
	if q := m.mainChat().Queued(); len(q) != 1 || q[0] != "check the tests" {
		t.Errorf("unstaged item should leave the queue, got %v", q)
	}
}

// Queued lines render above the prompt, so they must reserve viewport rows or
// they'd push the status bar off-screen.
func TestQueuedLinesReserveViewportRows(t *testing.T) {
	m := New(nil, false)
	m = m.SeedAssistantMarkdown("prior reply\n")
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	h0 := m.mainChat().Height()
	m.mainChat().Enqueue("one", nil)
	m.mainChat().Enqueue("two", nil)
	m.relayout()
	if got := m.mainChat().Height(); got != h0-2 {
		t.Errorf("two queued lines should reserve two rows: %d -> %d, want %d", h0, got, h0-2)
	}
}

type queuedPageStub struct{ body string }

func (p *queuedPageStub) ID() contentPageID                      { return contentPageContext }
func (p *queuedPageStub) SetSize(width, height int)              {}
func (p *queuedPageStub) Update(tea.KeyPressMsg) (tea.Cmd, bool) { return nil, false }
func (p *queuedPageStub) View() string                           { return p.body }

// Queued prompts belong to the main chat page. They must stay pending while a
// content page such as /c is open, but they should not render inside that page's
// chrome.
func TestQueuedLinesHiddenWhileContentPageActive(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.mainChat().Enqueue("queued secret prompt", nil)

	if view := m.View().Content; !strings.Contains(view, "queued secret prompt") {
		t.Fatal("queued prompt should render above the prompt on the main chat page")
	}

	m.content = &queuedPageStub{body: "context page body"}
	m.relayout()
	view := m.View().Content
	if !strings.Contains(view, "context page body") {
		t.Fatal("content page body should render")
	}
	if strings.Contains(view, "queued secret prompt") {
		t.Fatal("queued prompt should stay hidden while a content page is active")
	}
	if q := m.mainChat().Queued(); len(q) != 1 || q[0] != "queued secret prompt" {
		t.Fatalf("queued prompt should remain pending, got %v", q)
	}
}
