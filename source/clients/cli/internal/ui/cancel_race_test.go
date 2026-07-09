package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The ghost-turn race: cancel a prompt, immediately send another. The canceled
// turn's stream still has two events in flight — its "context canceled" error
// and its channel-close (streamEndMsg). Without turn identity on those events
// they pass the m.streaming guard (the new turn re-set it) and the stale
// streamEndMsg runs the completion path: it flips streaming off and calls
// m.cancelStream — which now holds the NEW turn's cancel func — killing the
// fresh prompt. Events must be fenced by turn generation.
func TestCancel_GhostEventsFromCanceledTurn_DoNotKillNextTurn(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Turn 1 in flight.
	m.streaming = true
	m.cancelStream = func() {}
	m.mainChat().AppendEntry(&Entry{Role: RoleUser, Content: "first prompt"})
	m.mainChat().AppendEntry(&Entry{Role: RoleAssistant, Content: "", Streaming: true})
	oldGen := m.turnGen

	// Esc cancels turn 1.
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)

	// Turn 2 starts before turn 1's ghost events arrive (what submit() does).
	m.turnGen++
	newTurnCanceled := false
	m.cancelStream = func() { newTurnCanceled = true }
	m.streaming = true
	m.mainChat().SetStreaming(true)
	m.mainChat().AppendEntry(&Entry{Role: RoleUser, Content: "second prompt"})
	m.mainChat().AppendEntry(&Entry{Role: RoleAssistant, Content: "", Streaming: true})

	// Turn 1's ghosts land: its cancel error, then its channel close.
	next, _ = m.Update(chatStreamMsg{gen: oldGen, ev: chatErrorMsg{err: errors.New("rpc error: code = Canceled desc = context canceled")}})
	m = next.(Model)
	next, _ = m.Update(streamEndMsg{gen: oldGen})
	m = next.(Model)

	if !m.streaming {
		t.Error("stale streamEndMsg killed the new turn's streaming state")
	}
	if newTurnCanceled {
		t.Error("stale streamEndMsg invoked the NEW turn's cancel func")
	}
	if m.cancelStream == nil {
		t.Error("stale streamEndMsg cleared the new turn's cancel slot")
	}
	for _, e := range m.mainChat().Entries() {
		if strings.Contains(e.Content, "context canceled") {
			t.Errorf("canceled turn's ghost error was painted into the new turn's transcript: %q", e.Content)
		}
	}
}

// The fence must not overshoot: the live turn's own end event still finalizes.
func TestCancel_CurrentTurnEndStillFinalizes(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.streaming = true
	released := false
	m.cancelStream = func() { released = true }
	m.mainChat().AppendEntry(&Entry{Role: RoleAssistant, Content: "answer", Streaming: true})

	next, _ := m.Update(streamEndMsg{gen: m.turnGen})
	m = next.(Model)

	if m.streaming {
		t.Error("live turn's streamEndMsg should stop streaming")
	}
	if !released {
		t.Error("live turn's streamEndMsg should release the stream context")
	}
}

// Cancel with no follow-up prompt: the canceled turn's late channel-close must
// be inert — in particular it must not re-run the completion path (which
// drains queued messages and fires polls) for a turn the user already killed.
func TestCancel_StaleEndAfterPlainCancel_IsInert(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.streaming = true
	m.cancelStream = func() {}
	m.lastSubmittedPrompt = "recoverable prompt"
	oldGen := m.turnGen

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)

	next, _ = m.Update(streamEndMsg{gen: oldGen})
	m = next.(Model)

	if m.lastSubmittedPrompt != "recoverable prompt" {
		t.Error("stale streamEndMsg cleared the crash-rehydration prompt cache")
	}
}
