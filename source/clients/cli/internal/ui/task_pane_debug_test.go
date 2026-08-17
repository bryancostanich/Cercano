package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func debugModeModel() Model {
	m := New(nil, false)
	m.workDirOverride = "/tmp/cercano-debug"
	return m
}

func TestDebugTaskPaneRejectsOutsideDevMode(t *testing.T) {
	m := New(nil, false)
	got := m.runDebugTaskPaneSlash([]string{"task", "seed", "basic"})
	if !strings.Contains(got, "only available after /d") {
		t.Fatalf("expected dev-mode rejection, got %q", got)
	}
	if _, err := m.runDebugTaskPaneTool(`{"op":"seed","scenario":"basic"}`); err == nil || !strings.Contains(err.Error(), "only available after /d") {
		t.Fatalf("expected tool dev-mode rejection, got %v", err)
	}
}

func TestDebugTaskPaneHelp(t *testing.T) {
	m := debugModeModel()
	got := m.runDebugTaskPaneSlash([]string{"help"})
	for _, want := range []string{"/debug task show|hide|toggle|clear", "seed <basic|nested|overflow|wrap|all-states>", debugTaskPaneToolName} {
		if !strings.Contains(got, want) {
			t.Fatalf("help missing %q: %q", want, got)
		}
	}
}

func TestDebugTaskPaneSeedShowHideToggleClear(t *testing.T) {
	m := debugModeModel()
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	msg, err := m.runDebugTaskPaneArgs([]string{"seed", "basic"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "seeded: basic") || !m.taskPaneHasTasks() || !m.taskPane.Expanded {
		t.Fatalf("seed basic failed: msg=%q expanded=%v tasks=%d", msg, m.taskPane.Expanded, len(m.taskPane.Tasks))
	}
	if _, err := m.runDebugTaskPaneArgs([]string{"hide"}); err != nil {
		t.Fatal(err)
	}
	if m.taskPane.Expanded {
		t.Fatal("hide should collapse pane")
	}
	if _, err := m.runDebugTaskPaneArgs([]string{"show"}); err != nil {
		t.Fatal(err)
	}
	if !m.taskPane.Expanded {
		t.Fatal("show should expand pane")
	}
	if _, err := m.runDebugTaskPaneArgs([]string{"toggle"}); err != nil {
		t.Fatal(err)
	}
	if m.taskPane.Expanded {
		t.Fatal("toggle should collapse expanded pane")
	}
	if _, err := m.runDebugTaskPaneArgs([]string{"clear"}); err != nil {
		t.Fatal(err)
	}
	if m.taskPaneHasTasks() || m.taskPane.Expanded {
		t.Fatalf("clear should remove and hide tasks: expanded=%v tasks=%d", m.taskPane.Expanded, len(m.taskPane.Tasks))
	}
}

func TestDebugTaskPaneScenarios(t *testing.T) {
	for _, scenario := range debugTaskPaneScenarios {
		m := debugModeModel()
		m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
		if _, err := m.runDebugTaskPaneArgs([]string{"seed", scenario}); err != nil {
			t.Fatalf("seed %s: %v", scenario, err)
		}
		if !m.taskPaneHasTasks() || !m.taskPane.Expanded {
			t.Fatalf("seed %s should create visible tasks", scenario)
		}
	}
}

func TestDebugTaskPaneCRUD(t *testing.T) {
	m := debugModeModel()
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	if _, err := m.runDebugTaskPaneArgs([]string{"add", "debug:root", "pending", "Root", "task"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.runDebugTaskPaneArgs([]string{"add-child", "debug:root", "debug:child", "pending", "Child", "task"}); err != nil {
		t.Fatal(err)
	}
	root := m.taskPane.Tasks["debug:root"]
	if len(root.Children) != 1 || root.Children[0] != "debug:child" {
		t.Fatalf("child not attached: %+v", root)
	}
	if _, err := m.runDebugTaskPaneArgs([]string{"status", "debug:child", "done"}); err != nil {
		t.Fatal(err)
	}
	if got := m.taskPane.Tasks["debug:child"].Status; got != "done" {
		t.Fatalf("child status = %q", got)
	}
	if _, err := m.runDebugTaskPaneArgs([]string{"title", "debug:child", "Renamed", "child"}); err != nil {
		t.Fatal(err)
	}
	if got := m.taskPane.Tasks["debug:child"].Title; got != "Renamed child" {
		t.Fatalf("child title = %q", got)
	}
	if _, err := m.runDebugTaskPaneArgs([]string{"remove", "debug:root"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.taskPane.Tasks["debug:root"]; ok {
		t.Fatal("root should be removed")
	}
	if _, ok := m.taskPane.Tasks["debug:child"]; ok {
		t.Fatal("child subtree should be removed")
	}
}

func TestDebugTaskPaneValidationErrors(t *testing.T) {
	m := debugModeModel()
	if _, err := m.runDebugTaskPaneArgs([]string{"seed", "missing"}); err == nil || !strings.Contains(err.Error(), "unknown scenario") {
		t.Fatalf("expected unknown scenario error, got %v", err)
	}
	if _, err := m.runDebugTaskPaneArgs([]string{"add", "debug:x", "weird", "Bad"}); err == nil || !strings.Contains(err.Error(), "invalid status") {
		t.Fatalf("expected invalid status error, got %v", err)
	}
	if _, err := m.runDebugTaskPaneArgs([]string{"add-child", "missing", "debug:x", "pending", "Bad"}); err == nil || !strings.Contains(err.Error(), "parent task") {
		t.Fatalf("expected missing parent error, got %v", err)
	}
}

func TestDebugTaskPaneSlashIntegration(t *testing.T) {
	m := debugModeModel()
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	next, _ := m.runSlash("/debug task seed basic")
	m = next.(Model)
	if !m.taskPaneHasTasks() || !m.taskPane.Expanded {
		t.Fatal("/debug task seed basic should create and show debug tasks")
	}
	entries := m.mainChat().Entries()
	if len(entries) == 0 || !strings.Contains(entries[len(entries)-1].Content, "seeded: basic") {
		t.Fatalf("/debug should append status notice, got %+v", entries)
	}
}

func TestDebugTaskPaneToolSlashBridge(t *testing.T) {
	m := debugModeModel()
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	next, _ := m.runSlash(`/tool debug_task_pane {"op":"seed","scenario":"wrap"}`)
	m = next.(Model)
	if !m.taskPaneHasTasks() || !m.taskPane.Expanded {
		t.Fatal("/tool debug_task_pane should create and show debug tasks")
	}
	if _, ok := m.taskPane.Tasks["debug:wrap:1"]; !ok {
		t.Fatalf("wrap scenario missing debug task: %#v", m.taskPane.Tasks)
	}
}

func TestDebugTaskPaneToolMatchesController(t *testing.T) {
	m := debugModeModel()
	m = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	msg, err := m.runDebugTaskPaneTool(`{"op":"seed","scenario":"overflow"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "seeded: overflow") || len(m.taskPane.Tasks) != 40 {
		t.Fatalf("tool seed overflow failed: msg=%q tasks=%d", msg, len(m.taskPane.Tasks))
	}
	msg, err = m.runDebugTaskPaneTool(`{"op":"status","id":"debug:overflow:01","status":"done"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "status updated") || m.taskPane.Tasks["debug:overflow:01"].Status != "done" {
		t.Fatalf("tool status failed: msg=%q task=%+v", msg, m.taskPane.Tasks["debug:overflow:01"])
	}
}

func TestDevModeNoticeMentionsDebugHelp(t *testing.T) {
	m := New(nil, false)
	_ = m.applyDevMode("/tmp/cercano")
	entries := m.mainChat().Entries()
	if len(entries) == 0 || !strings.Contains(entries[len(entries)-1].Content, "Try /debug help") {
		t.Fatalf("dev mode notice should mention debug help: %+v", entries)
	}
}
