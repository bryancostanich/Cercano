package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/slash"
)

func TestEffectiveWorkDirDefaultsToCwd(t *testing.T) {
	m := &Model{}
	wd, _ := os.Getwd()
	if got := m.effectiveWorkDir(); got != wd {
		t.Fatalf("got %q, want cwd %q", got, wd)
	}
}

func TestEffectiveWorkDirUsesOverride(t *testing.T) {
	m := &Model{workDirOverride: "/tmp/cercano-repo"}
	if got := m.effectiveWorkDir(); got != "/tmp/cercano-repo" {
		t.Fatalf("got %q, want override", got)
	}
}

func TestApplyDevMode(t *testing.T) {
	m := &Model{}
	kick := m.applyDevMode("/tmp/cercano-repo")
	if m.workDirOverride != "/tmp/cercano-repo" {
		t.Fatalf("override = %q, want /tmp/cercano-repo", m.workDirOverride)
	}
	if !strings.Contains(kick, "docs/agent/self-dev.md") {
		t.Fatalf("kickoff missing doc pointer: %q", kick)
	}
	// A visible system entry announces the mode switch.
	entries := m.mainChat().Entries()
	if len(entries) == 0 || !strings.Contains(entries[len(entries)-1].Content, "/tmp/cercano-repo") {
		t.Fatalf("no dev-mode system entry appended: %+v", entries)
	}
}

func TestRenderDevChip(t *testing.T) {
	off := Model{}
	if got := off.renderDevChip(); got != "" {
		t.Fatalf("chip should be empty when dev mode off, got %q", got)
	}
	on := Model{workDirOverride: "/tmp/cercano-repo"}
	if got := on.renderDevChip(); !strings.Contains(got, "DEV") {
		t.Fatalf("chip missing DEV label: %q", got)
	}
}

// makeDevRepo creates a minimal temp directory satisfying the Cercano repo
// markers (mirrors the helper in slash/dev_test.go).
func makeDevRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	serverDir := filepath.Join(root, "source", "server", "cmd", "cercano")
	if err := os.MkdirAll(serverDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cliDir := filepath.Join(root, "source", "clients", "cli")
	if err := os.MkdirAll(cliDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestDevModeStreamingQueuesKickoff asserts that when /d is run while a stream
// is in flight, the kickoff is enqueued (not submitted) and workDirOverride is
// set. No agent is attached, which also proves no submit was attempted.
func TestDevModeStreamingQueuesKickoff(t *testing.T) {
	repo := makeDevRepo(t)

	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	// Replace registry with one containing only RegisterDev (no agent needed).
	reg := slash.New()
	slash.RegisterDev(reg)
	m.registry = reg
	m.streaming = true

	got, _ := m.runSlash("/d " + repo)
	next := got.(Model)

	if next.workDirOverride != repo {
		t.Fatalf("workDirOverride = %q, want %q", next.workDirOverride, repo)
	}
	q := next.mainChat().Queued()
	if len(q) == 0 {
		t.Fatal("kickoff not enqueued while streaming")
	}
	if !strings.Contains(q[0], "development mode") {
		t.Fatalf("queued text doesn't look like a kickoff: %q", q[0])
	}
}

// TestClearResetsDevModeOverride asserts that /clear clears workDirOverride.
func TestClearResetsDevModeOverride(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	reg := slash.New()
	slash.RegisterBasics(reg)
	m.registry = reg
	m.workDirOverride = "/some/dev/repo"

	got, _ := m.runSlash("/clear")
	next := got.(Model)

	if next.workDirOverride != "" {
		t.Fatalf("workDirOverride not cleared after /clear: %q", next.workDirOverride)
	}
}
