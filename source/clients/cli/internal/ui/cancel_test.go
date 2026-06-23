package ui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/server/pkg/agentclient"
)

// Esc during an in-flight prompt cancels it: streaming stops, the context is
// canceled, and a "canceled" note is shown. Late stream messages are ignored.
func TestCancel_EscStopsStreaming(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Simulate an in-flight stream.
	_, cancel := context.WithCancel(context.Background())
	canceled := false
	m.cancelStream = func() { canceled = true; cancel() }
	m.streaming = true
	m.entries = append(m.entries,
		&Entry{Role: RoleUser, Content: "do a thing"},
		&Entry{Role: RoleAssistant, Content: "", Streaming: true})

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
	last := m.entries[len(m.entries)-1]
	if last.Role != RoleSystem || last.Content != "⊘ canceled" {
		t.Fatalf("expected a canceled note, got %+v", last)
	}

	// A late stream message after cancel must be ignored (no panic, no change).
	before := len(m.entries)
	next, _ = m.Update(streamTickMsg{msg: agentclient.StreamMsg{Type: agentclient.TypeToken, Token: "late"}})
	m = next.(Model)
	if len(m.entries) != before {
		t.Fatalf("late stream message after cancel should be ignored")
	}
}
