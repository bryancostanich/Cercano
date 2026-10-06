package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Regression: when the parent turn's stream is canceled (Esc, or a steering
// drain that calls cancelCurrentStreamSilently), every still-running child
// tab's events are retired by the turnGen bump — no done/error event will ever
// arrive. finishStaleSubAgentTabs must therefore clear the child view's own
// streaming state AND its in-progress tool rows, or the child tab keeps its
// "working" animation and spinner alive forever.
func TestFinishStaleSubAgentTabs_ClearsChildStreamingAndInProgressTools(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.applySubAgentEvent(subAgentEventMsg{id: "sa", kind: "started", tools: []string{"Read"}})
	// Streaming assistant text in the child view.
	m.applySubAgentEvent(subAgentEventMsg{id: "sa", kind: "token", inner: chatAssistantDeltaMsg{token: "partial"}})
	// An in-flight tool call that will never receive its completion event.
	m.applySubAgentEvent(subAgentEventMsg{id: "sa", kind: "token", inner: toolEntryStartMsg{id: "t1", name: "Read"}})

	tab := m.chatTabs.tabs["sa"]
	if tab == nil {
		t.Fatal("missing child tab")
	}
	if !tab.view.streaming {
		t.Fatal("precondition: child view should be streaming")
	}
	if !tab.view.hasInProgressTool() {
		t.Fatal("precondition: child view should have an in-progress tool")
	}

	m.finishStaleSubAgentTabs("sub-agent stopped without a terminal event")

	if !tab.done || !tab.errored {
		t.Fatalf("stale tab not finalized: done=%v errored=%v", tab.done, tab.errored)
	}
	if tab.view.streaming {
		t.Error("child view still streaming after stale finalization")
	}
	if tab.view.hasInProgressTool() {
		t.Error("child view still has an in-progress tool after stale finalization")
	}
	for _, e := range tab.view.Entries() {
		if e.Tool != nil && e.Tool.Status == ToolStatusInProgress {
			t.Errorf("tool row %s left in-progress", e.Tool.ToolUseID)
		}
	}
	if e := tab.view.lastAssistantEntry(); e != nil && e.Streaming {
		t.Error("child assistant entry still flagged Streaming after stale finalization")
	}
	foundReason := false
	for _, e := range tab.view.Entries() {
		if e.Role == RoleSystem && strings.Contains(e.Content, "sub-agent stopped without a terminal event") {
			foundReason = true
		}
	}
	if !foundReason {
		t.Error("stale finalization did not append the reason line to the child tab")
	}
}

// Regression: the steering path (DrainNext during streaming) cancels the
// current turn SILENTLY via cancelCurrentStreamWithNotice(false) and then
// submits a new turn. That cancel must finalize all still-running child tabs —
// clearing streaming and in-progress tools — BEFORE the new turn starts, since
// the turnGen bump guarantees the children's terminal events are ghosts.
func TestCancelCurrentStreamSilently_FinalizesChildTabsBeforeNewTurn(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.streaming = true
	m.cancelStream = func() {}
	m.applySubAgentEvent(subAgentEventMsg{id: "sa", kind: "started"})
	m.applySubAgentEvent(subAgentEventMsg{id: "sa", kind: "token", inner: chatAssistantDeltaMsg{token: "partial"}})
	m.applySubAgentEvent(subAgentEventMsg{id: "sa", kind: "token", inner: toolEntryStartMsg{id: "t1", name: "Bash"}})

	m.cancelCurrentStreamSilently()

	tab := m.chatTabs.tabs["sa"]
	if tab == nil {
		t.Fatal("missing child tab after cancel")
	}
	if !tab.done {
		t.Error("child tab not marked done by silent cancel")
	}
	if tab.view.streaming {
		t.Error("child view still streaming after silent cancel")
	}
	if tab.view.hasInProgressTool() {
		t.Error("child view still has an in-progress tool after silent cancel")
	}
	// The new-turn precondition: a subsequent turnGen bump (submit) sees no
	// live child tabs, so finishStaleSubAgentTabs is a no-op on the next turn.
	m.turnGen++
	m.finishStaleSubAgentTabs("sub-agent stopped without a terminal event")
	if !tab.done {
		t.Error("child tab state regressed across the new turn boundary")
	}
}

// The Esc-key cancel (with notice) goes through the same helper and must
// finalize children the same way.
func TestCancelCurrentStreamWithNotice_FinalizesChildTabs(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.streaming = true
	m.cancelStream = func() {}
	m.applySubAgentEvent(subAgentEventMsg{id: "sa", kind: "started"})
	m.applySubAgentEvent(subAgentEventMsg{id: "sa", kind: "token", inner: chatAssistantDeltaMsg{token: "partial"}})
	m.applySubAgentEvent(subAgentEventMsg{id: "sa", kind: "token", inner: toolEntryStartMsg{id: "t1", name: "Grep"}})

	m.cancelCurrentStreamWithNotice(true)

	tab := m.chatTabs.tabs["sa"]
	if tab == nil {
		t.Fatal("missing child tab after cancel")
	}
	if !tab.done || tab.view.streaming || tab.view.hasInProgressTool() {
		t.Errorf("child tab not cleaned up: done=%v streaming=%v inProgressTool=%v",
			tab.done, tab.view.streaming, tab.view.hasInProgressTool())
	}
	if !strings.Contains(m.mainChat().Entries()[len(m.mainChat().Entries())-1].Content, "canceled") {
		t.Error("notice variant should append the canceled notice to main")
	}
}

// Terminal done/error events must also resolve an in-progress tool row the
// child never completed, so the spinner tick cannot latch onto a finished tab.
func TestSubAgentTerminalEvent_ResolvesStaleInProgressTool(t *testing.T) {
	for _, kind := range []string{"done", "error"} {
		m := New(nil, false)
		m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
		// An activity tab survives its own done event (only dispatch sub-agent
		// tabs self-retire on done), so both terminal kinds are observable.
		id := "activity:check"
		m.applySubAgentEvent(subAgentEventMsg{id: id, kind: "started"})
		m.applySubAgentEvent(subAgentEventMsg{id: id, kind: "token", inner: toolEntryStartMsg{id: "t1", name: "Read"}})

		m.applySubAgentEvent(subAgentEventMsg{id: id, kind: kind, text: "boom"})

		tab := m.chatTabs.tabs[id]
		if tab == nil {
			t.Fatalf("kind %s: child tab missing after terminal event", kind)
		}
		if tab.view.hasInProgressTool() {
			t.Errorf("kind %s: in-progress tool survived terminal event", kind)
		}
		if tab.view.streaming {
			t.Errorf("kind %s: child view still streaming after terminal event", kind)
		}
	}
}

// Preserved behavior: finishStaleSubAgentTabs must not touch restored tabs or
// tabs already finished, and must keep them in the strip.
func TestFinishStaleSubAgentTabs_SparesDoneAndRestoredTabs(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.applySubAgentEvent(subAgentEventMsg{id: "live", kind: "started"})
	m.applySubAgentEvent(subAgentEventMsg{id: "done", kind: "started"})
	if tab := m.chatTabs.tabs["done"]; tab != nil {
		tab.done = true
	}
	m.applySubAgentEvent(subAgentEventMsg{id: "restored", kind: "started"})
	if tab := m.chatTabs.tabs["restored"]; tab != nil {
		tab.restored = true
	}

	m.finishStaleSubAgentTabs("sub-agent stopped without a terminal event")

	if tab := m.chatTabs.tabs["done"]; tab == nil || tab.errored {
		t.Error("already-done tab must be spared (not re-flagged errored)")
	}
	if tab := m.chatTabs.tabs["restored"]; tab == nil || tab.done || tab.errored {
		t.Error("restored tab must be spared by the stale finalizer")
	}
	if tab := m.chatTabs.tabs["live"]; tab == nil || !tab.done || !tab.errored {
		t.Error("running tab must be finalized by the stale finalizer")
	}
}
