package ui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/server/pkg/agentclient"
)

func seedOverflowingTaskPane(m *Model, n int) {
	for i := 0; i < n; i++ {
		m.applyTaskChange("added", &agentclient.TaskNode{ID: fmt.Sprintf("task-%02d", i), Title: fmt.Sprintf("Task %02d", i), Status: "pending"})
	}
}

func seedWideTaskPane(m *Model) {
	m.taskPane.Width = taskPaneMinWidth
	m.applyTaskChange("added", &agentclient.TaskNode{
		ID:     "long-task",
		Title:  "abcdefghijklmnopqrstuvwxyz-0123456789-this-title-is-longer-than-the-pane",
		Status: "pending",
	})
}

func TestTaskPaneVerticalScrollbarClickAndDrag(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	seedOverflowingTaskPane(&m, 40)
	m.toggleTaskPane()
	g, ok := m.taskPaneGeometry()
	if !ok || !g.needV {
		t.Fatalf("expected vertical scrollbar geometry: %+v ok=%v", g, ok)
	}

	m = send(t, m, tea.MouseClickMsg{X: g.vbarX(), Y: g.bodyTop() + g.bodyH - 1, Button: tea.MouseLeft})
	if !m.taskPane.Dragging || scrollbarOrientation(m.taskPane.Drag) != scrollbarVertical {
		t.Fatalf("vertical scrollbar click should start vertical drag, dragging=%v axis=%v", m.taskPane.Dragging, m.taskPane.Drag)
	}
	if m.taskPane.ScrollY == 0 {
		t.Fatal("vertical scrollbar click near bottom should move ScrollY")
	}
	first := m.taskPane.ScrollY

	m = send(t, m, tea.MouseMotionMsg{X: g.vbarX(), Y: g.bodyTop(), Button: tea.MouseLeft})
	if m.taskPane.ScrollY >= first {
		t.Fatalf("vertical scrollbar drag upward should reduce ScrollY: before=%d after=%d", first, m.taskPane.ScrollY)
	}
	m = send(t, m, tea.MouseReleaseMsg{X: g.vbarX(), Y: g.bodyTop(), Button: tea.MouseLeft})
	if m.taskPane.Dragging {
		t.Fatal("vertical scrollbar release should clear task pane drag")
	}
}

func TestTaskPaneWrappedTitlesDoNotExposeHorizontalScrollbarDrag(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	seedWideTaskPane(&m)
	m.toggleTaskPane()
	g, ok := m.taskPaneGeometry()
	if !ok {
		t.Fatalf("expected task pane geometry")
	}
	if g.needH {
		t.Fatalf("wrapped task titles should not expose horizontal scrollbar geometry: %+v", g)
	}
	m = send(t, m, tea.MouseClickMsg{X: g.contentLeft() + g.contentW - 1, Y: g.hbarY(), Button: tea.MouseLeft})
	if m.taskPane.Dragging {
		t.Fatal("without horizontal overflow, click near former hbar row should not start scrollbar drag")
	}
}

func TestTaskPaneExpandedRailClickCollapsesBodyClickDoesNot(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	seedTaskPane(&m)
	m.toggleTaskPane()
	g, ok := m.taskPaneGeometry()
	if !ok {
		t.Fatal("expected task pane geometry")
	}

	m = send(t, m, tea.MouseClickMsg{X: g.contentLeft() + 2, Y: g.bodyTop(), Button: tea.MouseLeft})
	if !m.taskPane.Expanded {
		t.Fatal("body click should not collapse expanded task pane")
	}
	if m.activeChat().SelectionDragging() {
		t.Fatal("body click inside task pane should not start chat selection behind pane")
	}

	m = send(t, m, tea.MouseClickMsg{X: g.left, Y: g.top, Button: tea.MouseLeft})
	if m.taskPane.Expanded {
		t.Fatal("expanded rail/header click should collapse task pane")
	}
}
