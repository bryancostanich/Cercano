package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildToolLoopSystem(t *testing.T) {
	env := loopEnv{
		WorkDir: "/home/u/proj", Platform: "darwin", Date: "2026-06-24",
		GitRepo: true, GitBranch: "main",
	}
	s := buildToolLoopSystem(env, "", "Cercano/\nkhalkulo/\nnotes.txt", "Cercano is a local-first AI dev tool.")

	for _, want := range []string{
		"/home/u/proj", "darwin", "2026-06-24", "branch main",
		"Cercano/", "notes.txt", "Cercano is a local-first AI dev tool.",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("system prompt missing %q in:\n%s", want, s)
		}
	}
}

func TestBuildToolLoopSystem_NoGitNoExtras(t *testing.T) {
	s := buildToolLoopSystem(loopEnv{WorkDir: "/p", Platform: "linux", Date: "2026-06-24"}, "", "", "")
	if !strings.Contains(s, "Is a git repository: no") {
		t.Errorf("expected non-repo line, got:\n%s", s)
	}
	if strings.Contains(s, "<directory>") {
		t.Errorf("empty snapshot should omit the directory block:\n%s", s)
	}
}

func TestDirectorySnapshot(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"Cercano", "khalkulo"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"notes.txt", ".hidden"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snap := directorySnapshot(dir, 60)

	if !strings.Contains(snap, "Cercano/") || !strings.Contains(snap, "khalkulo/") {
		t.Errorf("directories should be listed with trailing slash: %q", snap)
	}
	if !strings.Contains(snap, "notes.txt") {
		t.Errorf("files should be listed: %q", snap)
	}
	if strings.Contains(snap, ".hidden") {
		t.Errorf("dotfiles should be skipped: %q", snap)
	}
	if strings.Index(snap, "Cercano/") > strings.Index(snap, "notes.txt") {
		t.Errorf("directories should come before files: %q", snap)
	}
}

func TestDirectorySnapshot_Truncates(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		if err := os.WriteFile(filepath.Join(dir, string(rune('a'+i))+".txt"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snap := directorySnapshot(dir, 3)
	if !strings.Contains(snap, "more)") {
		t.Errorf("over-cap snapshot should note the truncation: %q", snap)
	}
}
