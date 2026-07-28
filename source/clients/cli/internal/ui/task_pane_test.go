package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestTaskPaneCollapsedReservesOneColumnWhenWide(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
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

func TestTaskPaneRenderShowsCollapsedTabAndExpandedPlaceholder(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})
	collapsed := m.renderTaskPane(taskPaneCollapsedWidth, 8)
	if !strings.Contains(collapsed, "◀") || !strings.Contains(collapsed, "T") {
		t.Fatalf("collapsed pane should show arrow/tab label, got %q", collapsed)
	}
	m.toggleTaskPane()
	expanded := m.renderTaskPane(m.taskPaneWidth(), 8)
	if !strings.Contains(expanded, "▶ Tasks") || !strings.Contains(expanded, "No task tree loaded yet") {
		t.Fatalf("expanded pane missing header/placeholder:\n%s", expanded)
	}
}

func TestTaskPaneMouseHitToggles(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})
	if !m.taskPaneHit(119, m.scrollbarTop) {
		t.Fatal("right-edge tab should be hittable")
	}
	m = send(t, m, tea.MouseClickMsg{X: 119, Y: m.scrollbarTop, Button: tea.MouseLeft})
	if !m.taskPane.Expanded {
		t.Fatal("clicking the right-edge tab should expand the pane")
	}
}
