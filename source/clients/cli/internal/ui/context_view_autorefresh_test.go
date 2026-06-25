package ui

import (
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func TestContextAutoRefresh_SnapshotMsgApplied(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	snap := contextSnapshot{Turns: []agentclient.ContextTurn{{ID: "a", Role: "user", Preview: "x"}}}
	if _, _ = m.Update(contextSnapshotMsg{snap: snap}); len(cv.snapshot.Turns) != 1 {
		t.Errorf("contextSnapshotMsg should update the live snapshot; got %d turns", len(cv.snapshot.Turns))
	}
}

func TestContextAutoRefresh_TickReschedulesWhileOpenStopsWhenClosed(t *testing.T) {
	m := modelWithContextView()
	if _, cmd := m.Update(contextRefreshTickMsg{}); cmd == nil {
		t.Error("tick should reschedule (non-nil cmd) while /c is open")
	}
	m.content = nil // /c closed
	if _, cmd := m.Update(contextRefreshTickMsg{}); cmd != nil {
		t.Error("tick should stop (nil cmd) once /c is closed")
	}
}
