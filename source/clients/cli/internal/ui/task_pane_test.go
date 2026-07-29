package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/server/pkg/agentclient"
)

func seedTaskPane(m *Model) {
	m.applyTaskChange("added", &agentclient.TaskNode{ID: "task-1", Title: "Do the thing", Status: "pending"})
}

func seedTaskPaneTree(m *Model) {
	m.applyTaskChange("added", &agentclient.TaskNode{
		ID:     "phase-1",
		Title:  "Phase 1",
		Status: "pending",
		Children: []agentclient.TaskNode{
			{ID: "task-1", Title: "Do the thing", Status: "in_progress", ParentID: "phase-1", Children: []agentclient.TaskNode{
				{ID: "subtask-1", Title: "Check the result", Status: "done", ParentID: "task-1"},
			}},
			{ID: "task-2", Title: "Unblock follow-up", Status: "blocked", ParentID: "phase-1"},
		},
	})
}

func TestTaskPaneHiddenUntilTasksExist(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	if got := m.taskPaneWidth(); got != 0 {
		t.Fatalf("pane should be hidden without tasks, got width %d", got)
	}
	if got := m.mainChat().Width(); got != m.width-2 {
		t.Fatalf("chat width should not reserve a task tab without tasks: got %d want %d", got, m.width-2)
	}
	m.toggleTaskPane()
	if m.taskPane.Expanded {
		t.Fatal("toggle should be ignored while no tasks exist")
	}
}

func TestTaskPaneCollapsedReservesOneColumnWhenWide(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	seedTaskPane(&m)
	if got := m.taskPaneWidth(); got != taskPaneCollapsedWidth {
		t.Fatalf("collapsed pane width = %d, want %d", got, taskPaneCollapsedWidth)
	}
	// Chat view width includes its own scrollbar reservation: relayout sets
	// active chat width to contentW-2, and contentW is terminal width minus the
	// pane reserve.
	wantChatW := 120 - taskPaneCollapsedWidth - 2
	if got := m.mainChat().Width(); got != wantChatW {
		t.Fatalf("chat width = %d, want %d", got, wantChatW)
	}
}

func TestTaskPaneUnavailableWhenNarrow(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: taskPaneMinTerminalWidth - 1, Height: 24})
	seedTaskPane(&m)
	if m.taskPaneWidth() != 0 {
		t.Fatalf("pane should be unavailable below min width, got %d", m.taskPaneWidth())
	}
	if got := m.mainChat().Width(); got != m.width-2 {
		t.Fatalf("narrow chat width = %d, want %d", got, m.width-2)
	}
}

func TestTaskPaneToggleExpandsAndReclaimsWidth(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	seedTaskPane(&m)
	collapsedChatW := m.mainChat().Width()
	m.toggleTaskPane()
	if !m.taskPane.Expanded {
		t.Fatal("toggle should expand pane")
	}
	if got := m.taskPaneWidth(); got != taskPaneDefaultWidth {
		t.Fatalf("expanded pane width = %d, want %d", got, taskPaneDefaultWidth)
	}
	if m.mainChat().Width() >= collapsedChatW {
		t.Fatalf("expanded pane should shrink chat width: before=%d after=%d", collapsedChatW, m.mainChat().Width())
	}
	m.toggleTaskPane()
	if m.taskPane.Expanded {
		t.Fatal("second toggle should collapse pane")
	}
	if got := m.mainChat().Width(); got != collapsedChatW {
		t.Fatalf("collapsed chat width = %d, want restored %d", got, collapsedChatW)
	}
}

func TestTaskPaneRenderShowsCollapsedTabAndExpandedTree(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})
	seedTaskPaneTree(&m)
	collapsed := m.renderTaskPane(taskPaneCollapsedWidth, 8)
	if !strings.Contains(collapsed, "◀") || !strings.Contains(collapsed, "T") {
		t.Fatalf("collapsed pane should show arrow/tab label, got %q", collapsed)
	}
	m.toggleTaskPane()
	expanded := m.renderTaskPane(m.taskPaneWidth(), 10)
	plainExpanded := ansi.Strip(expanded)
	for _, want := range []string{"▶ Tasks", "☐ Phase 1", "~ Do the thing", "✓ Check the result", "! Unblock follow-up"} {
		if !strings.Contains(plainExpanded, want) {
			t.Fatalf("expanded pane missing %q:\n%s", want, expanded)
		}
	}
	if strings.Contains(plainExpanded, "No task tree loaded yet") {
		t.Fatalf("expanded pane should render real tasks, not placeholder:\n%s", expanded)
	}
}

func TestTaskPaneMouseHitToggles(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})
	seedTaskPane(&m)
	if !m.taskPaneHit(119, m.scrollbarTop) {
		t.Fatal("right-edge tab should be hittable")
	}
	m = send(t, m, tea.MouseClickMsg{X: 119, Y: m.scrollbarTop, Button: tea.MouseLeft})
	if !m.taskPane.Expanded {
		t.Fatal("clicking the right-edge tab should expand the pane")
	}
}

func TestTaskPaneRemovalHidesPane(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})
	seedTaskPane(&m)
	m.toggleTaskPane()
	m.applyTaskChange("removed", &agentclient.TaskNode{ID: "task-1", Title: "Do the thing", Status: "pending"})
	if got := m.taskPaneWidth(); got != 0 {
		t.Fatalf("pane should hide after last task is removed, got width %d", got)
	}
	if m.taskPane.Expanded {
		t.Fatal("pane should collapse when the last task disappears")
	}
}

func TestTaskPaneRendersDocumentOrderHierarchy(t *testing.T) {
	m := New(nil, false)
	seedTaskPaneTree(&m)
	lines := ansi.Strip(strings.Join(m.taskPaneLines(34), "\n"))
	phase := strings.Index(lines, "Phase 1")
	task := strings.Index(lines, "Do the thing")
	subtask := strings.Index(lines, "Check the result")
	blocked := strings.Index(lines, "Unblock follow-up")
	if phase < 0 || task < 0 || subtask < 0 || blocked < 0 {
		t.Fatalf("missing expected tree lines:\n%s", lines)
	}
	if !(phase < task && task < subtask && subtask < blocked) {
		t.Fatalf("tree should render in document order, got:\n%s", lines)
	}
	if !strings.Contains(lines, "  ~ Do the thing") || !strings.Contains(lines, "    ✓ Check the result") {
		t.Fatalf("tree should render nested indentation, got:\n%s", lines)
	}
}
