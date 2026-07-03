package ui

import (
	"os"
	"strings"
	"testing"
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
	entries := m.chat.Entries()
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
