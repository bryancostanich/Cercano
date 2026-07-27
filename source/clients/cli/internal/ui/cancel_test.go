package ui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/server/pkg/agentclient"
)

// Canceling a turn while a compaction is in progress must clear the compacting
// flag and resolve any stuck in-progress tool. Both latch the 50ms animation
// tick on; leaving either set after the turn ends pins a CPU core until the
// process restarts (the length-independent, restart-cured lag bug).
func TestCancel_ClearsLatchedCompactingAndInProgressTool(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.cancelStream = func() {}
	m.streaming = true
	m.compacting = true // server reported compaction; closing update never arrives
	m.mainChat().AppendEntry(&Entry{Role: RoleAssistant, Content: "working", Streaming: true})
	m.mainChat().AppendEntry(&Entry{Role: RoleSystem, Content: "", Tool: &ToolEntry{ToolName: "research", Status: ToolStatusInProgress}})

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)

	if m.compacting {
		t.Fatal("cancel must clear the latched compacting flag")
	}
	if m.mainChat().hasInProgressTool() {
		t.Fatal("cancel must resolve stuck in-progress tools")
	}
}

// A normal stream end must also clear a latched compacting flag: if the closing
// ctxUsageMsg{Compacting:false} was dropped, streamEnd is the turn's real
// terminator and has to reset the animation-driving state.
func TestStreamEnd_ClearsLatchedCompacting(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.streaming = true
	m.compacting = true
	gen := m.turnGen

	next, _ := m.Update(streamEndMsg{gen: gen})
	m = next.(Model)

	if m.compacting {
		t.Fatal("stream end must clear the latched compacting flag")
	}
}

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
