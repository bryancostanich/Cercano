package ui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/server/pkg/agentclient"
)

// Esc during an in-flight prompt cancels it: streaming stops, the context is
// canceled, and a "canceled" note is shown. Late stream messages are ignored.
func TestStreamingEnterCancelsCurrentTurnBeforeSubmittingSteer(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	canceled := false
	m.cancelStream = func() { canceled = true }
	m.streaming = true
	m.input.Placeholder = steerInputPlaceholder
	m.input.SetValue("actually do the other thing")
	m.mainChat().AppendEntry(&Entry{Role: RoleUser, Content: "do a thing"})
	m.mainChat().AppendEntry(&Entry{Role: RoleAssistant, Content: "working", Streaming: true})

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: "\n"})
	m = next.(Model)

	if !canceled {
		t.Fatal("enter with steering text should cancel current stream")
	}
	if queued, ok := m.mainChat().DrainNext(); ok {
		t.Fatalf("steering should not enqueue behind current stream, got %+v", queued)
	}
	foundCanceled := false
	for _, e := range m.mainChat().Entries() {
		if e.Role == RoleSystem && e.Content == "⊘ canceled" {
			foundCanceled = true
		}
	}
	if !foundCanceled {
		t.Fatal("steering should mark the interrupted turn canceled")
	}
}

func TestCancel_EscStopsStreaming(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Simulate an in-flight stream.
	_, cancel := context.WithCancel(context.Background())
	canceled := false
	m.cancelStream = func() { canceled = true; cancel() }
	m.streaming = true
	m.mainChat().AppendEntry(&Entry{Role: RoleUser, Content: "do a thing"})
	m.mainChat().AppendEntry(&Entry{Role: RoleAssistant, Content: "", Streaming: true})

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)

	if m.streaming {
		t.Fatalf("Esc should stop streaming")
	}
	if !canceled {
		t.Fatalf("Esc should cancel the stream context")
	}
	if m.cancelStream != nil {
		t.Fatalf("cancelStream should be cleared")
	}
	entries := m.mainChat().Entries()
	last := entries[len(entries)-1]
	if last.Role != RoleSystem || last.Content != "⊘ canceled" {
		t.Fatalf("expected a canceled note, got %+v", last)
	}

	// A late stream event after cancel must be ignored (no panic, no change).
	before := len(m.mainChat().Entries())
	next, _ = m.Update(chatStreamMsg{ev: streamMsgToEvent(agentclient.StreamMsg{Type: agentclient.TypeToken, Token: "late"})})
	m = next.(Model)
	if len(m.mainChat().Entries()) != before {
		t.Fatalf("late stream message after cancel should be ignored")
	}
}
