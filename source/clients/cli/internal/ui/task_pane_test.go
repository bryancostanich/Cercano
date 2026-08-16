package ui

import (
	"fmt"
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
	if m.taskPaneToggleHit(118, m.scrollbarTop) {
		t.Fatal("collapsed task pane should not toggle from the main scrollback scrollbar column")
	}
	m = send(t, m, tea.MouseClickMsg{X: 118, Y: m.scrollbarTop, Button: tea.MouseLeft})
	if m.taskPane.Expanded {
		t.Fatal("clicking outside the right-edge TASKS tab should not expand the pane")
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

func TestTaskPaneWrapsLongTitlesWithIndentation(t *testing.T) {
	m := New(nil, false)
	m.applyTaskChange("added", &agentclient.TaskNode{
		ID:     "parent",
		Title:  "Parent task with a title that needs to wrap inside the task pane",
		Status: "pending",
		Children: []agentclient.TaskNode{{
			ID:       "child",
			Title:    "Child task also wraps while preserving nested indentation",
			Status:   "pending",
			ParentID: "parent",
		}},
	})
	lines := strings.Split(ansi.Strip(strings.Join(m.taskPaneLines(24), "\n")), "\n")
	if len(lines) < 4 {
		t.Fatalf("expected wrapped task lines, got %#v", lines)
	}
	if !strings.HasPrefix(lines[0], "☐ ") || !strings.HasPrefix(lines[1], "  ") {
		t.Fatalf("parent continuation should align under title text, got %#v", lines[:2])
	}
	childStart := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "  ☐ ") {
			childStart = i
			break
		}
	}
	if childStart < 0 || childStart+1 >= len(lines) {
		t.Fatalf("expected wrapped child task lines, got %#v", lines)
	}
	if !strings.HasPrefix(lines[childStart+1], "    ") {
		t.Fatalf("child continuation should preserve nested indentation, got %#v", lines[childStart:childStart+2])
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > 24 {
			t.Fatalf("wrapped line exceeds pane width: width=%d line=%q all=%#v", ansi.StringWidth(line), line, lines)
		}
	}
}

func TestTaskPaneLongTitlesWrapInsteadOfHorizontalScrolling(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m.taskPane.Width = taskPaneMinWidth
	m.applyTaskChange("added", &agentclient.TaskNode{
		ID:     "long-task",
		Title:  "abcdefghijklmnopqrstuvwxyz 0123456789 this title wraps rather than requiring horizontal scrolling",
		Status: "pending",
	})
	m.toggleTaskPane()
	_, _, _, needH, _, _ := m.taskPaneViewportGeometry(m.taskPaneWidth(), m.activeChat().Height())
	if needH {
		t.Fatal("wrappable task titles should not require a horizontal scrollbar")
	}
	view := ansi.Strip(m.renderTaskPane(m.taskPaneWidth(), m.activeChat().Height()))
	if strings.Contains(view, "░") || strings.Contains(view, "█") {
		t.Fatalf("wrapped title should not render a horizontal scrollbar:\n%s", view)
	}
	if !strings.Contains(view, "rather than") || !strings.Contains(view, "requiring") {
		t.Fatalf("wrapped title should reveal later text without horizontal scroll:\n%s", view)
	}
}

func TestTaskPaneVerticalScrollbarAndWheelScroll(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	for i := 0; i < 20; i++ {
		m.applyTaskChange("added", &agentclient.TaskNode{ID: fmt.Sprintf("task-%02d", i), Title: fmt.Sprintf("Task %02d", i), Status: "pending"})
	}
	m.toggleTaskPane()

	view := ansi.Strip(m.renderTaskPane(m.taskPaneWidth(), m.activeChat().Height()))
	if !strings.Contains(view, "░") || !strings.Contains(view, "█") {
		t.Fatalf("overflowing task pane should render a vertical scrollbar:\n%s", view)
	}
	if !strings.Contains(view, "Task 00") {
		t.Fatalf("expected top task before scrolling:\n%s", view)
	}

	paneX := m.width - m.taskPaneWidth() + 2
	m = send(t, m, tea.MouseWheelMsg{X: paneX, Y: m.scrollbarTop + 3, Button: tea.MouseWheelDown})
	if m.taskPane.ScrollY == 0 {
		t.Fatal("mouse wheel over expanded task pane should advance vertical task scroll")
	}
	scrolled := ansi.Strip(m.renderTaskPane(m.taskPaneWidth(), m.activeChat().Height()))
	if scrolled == view {
		t.Fatalf("scrolling should change visible task pane window:\n%s", scrolled)
	}
}

func TestTaskPaneHorizontalInputNoopsWhenTitlesWrap(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m.taskPane.Width = taskPaneMinWidth
	m.applyTaskChange("added", &agentclient.TaskNode{
		ID:     "long-task",
		Title:  "abcdefghijklmnopqrstuvwxyz-0123456789-this-title-is-longer-than-the-pane",
		Status: "pending",
	})
	m.toggleTaskPane()

	paneX := m.width - m.taskPaneWidth() + 2
	m = send(t, m, tea.MouseWheelMsg{X: paneX, Y: m.scrollbarTop + 3, Button: tea.MouseWheelRight})
	m = send(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.taskPane.ScrollX != 0 {
		t.Fatalf("wrapped task titles should not require horizontal scroll, got ScrollX=%d", m.taskPane.ScrollX)
	}
	view := ansi.Strip(m.renderTaskPane(m.taskPaneWidth(), m.activeChat().Height()))
	if !strings.Contains(view, "is-title-is-longer") || !strings.Contains(view, "han-the-pane") {
		t.Fatalf("wrapped title should reveal later text without horizontal scroll:\n%s", view)
	}
}
