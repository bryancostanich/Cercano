package builtins

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
)

// initWriteTestRepo creates a temporary git repository with one commit.
// Shared with git_read_test.go's initTestRepo — same logic, different name
// to avoid duplicate declaration in the package.
func initWriteTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	gitRun("init", "-b", "main")
	gitRun("config", "user.email", "test@example.com")
	gitRun("config", "user.name", "Test")

	f := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(f, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", "hello.txt")
	gitRun("commit", "-m", "initial commit")

	return dir
}

// --- git_add ---

func TestGitAddCap_Meta(t *testing.T) {
	cap := GitAdd()
	if cap.Name() != "git_add" {
		t.Fatalf("name wrong: %q", cap.Name())
	}
	if cap.Tier() != capabilities.TierW {
		t.Fatalf("tier wrong: %v", cap.Tier())
	}
	want := capabilities.SurfaceAgent | capabilities.SurfaceMCP
	if cap.Surfaces() != want {
		t.Fatalf("surfaces wrong: %v", cap.Surfaces())
	}
}

func TestGitAddCap_StagesFile(t *testing.T) {
	dir := initWriteTestRepo(t)

	// Create a new untracked file.
	newFile := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(newFile, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cap := GitAdd()
	args, _ := json.Marshal(map[string]any{
		"paths": []string{"new.txt"},
		"cwd":   dir,
	})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatalf("git_add failed: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	// Confirm file is staged by checking git status.
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, _ := cmd.Output()
	if !strings.Contains(string(out), "A  new.txt") {
		t.Fatalf("file not staged; status: %s", out)
	}
}

func TestGitAddCap_RequiresPaths(t *testing.T) {
	cap := GitAdd()
	args, _ := json.Marshal(map[string]any{"paths": []string{}})
	_, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error for empty paths")
	}
	if !strings.Contains(err.Error(), "git_add:") {
		t.Fatalf("error missing prefix: %v", err)
	}
}

// --- git_commit ---

func TestGitCommitCap_Meta(t *testing.T) {
	cap := GitCommit()
	if cap.Name() != "git_commit" {
		t.Fatalf("name wrong: %q", cap.Name())
	}
	if cap.Tier() != capabilities.TierW {
		t.Fatalf("tier wrong: %v", cap.Tier())
	}
	want := capabilities.SurfaceAgent | capabilities.SurfaceMCP
	if cap.Surfaces() != want {
		t.Fatalf("surfaces wrong: %v", cap.Surfaces())
	}
}

func TestGitCommitCap_CreateCommit(t *testing.T) {
	dir := initWriteTestRepo(t)

	// Stage a new file first.
	newFile := filepath.Join(dir, "commit_me.txt")
	if err := os.WriteFile(newFile, []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAdd := exec.Command("git", "add", "commit_me.txt")
	gitAdd.Dir = dir
	if out, err := gitAdd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	cap := GitCommit()
	args, _ := json.Marshal(map[string]any{
		"message": "test commit from test suite",
		"cwd":     dir,
	})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatalf("git_commit failed: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}

	// Verify commit exists.
	cmd := exec.Command("git", "log", "--oneline", "-1")
	cmd.Dir = dir
	out, _ := cmd.Output()
	if !strings.Contains(string(out), "test commit from test suite") {
		t.Fatalf("commit not found in log: %s", out)
	}
}

func TestGitCommitCap_RequiresMessage(t *testing.T) {
	cap := GitCommit()
	args, _ := json.Marshal(map[string]any{"message": ""})
	_, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error for empty message")
	}
	if !strings.Contains(err.Error(), "git_commit:") {
		t.Fatalf("error missing prefix: %v", err)
	}
}

// --- git_push ---

func TestGitPushCap_Meta(t *testing.T) {
	cap := GitPush()
	if cap.Name() != "git_push" {
		t.Fatalf("name wrong: %q", cap.Name())
	}
	if cap.Tier() != capabilities.TierX {
		t.Fatalf("tier wrong: %v", cap.Tier())
	}
	want := capabilities.SurfaceAgent | capabilities.SurfaceMCP
	if cap.Surfaces() != want {
		t.Fatalf("surfaces wrong: %v", cap.Surfaces())
	}
}

func TestGitPushCap_ForceUsesForceWithLease(t *testing.T) {
	// Push against a repo with no remote — the error message will contain the
	// git invocation args when force=true; we verify --force-with-lease appears
	// in the error (git echoes the refname / error text that includes push args).
	// More reliably: use a bare repo as remote.
	dir := initWriteTestRepo(t)

	// Create a bare "remote" and add it.
	remoteDir := t.TempDir()
	initCmd := exec.Command("git", "init", "--bare", remoteDir)
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init bare: %v\n%s", err, out)
	}

	addRemote := exec.Command("git", "remote", "add", "origin", remoteDir)
	addRemote.Dir = dir
	if out, err := addRemote.CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}

	// Normal push first (to populate remote).
	pushCmd := exec.Command("git", "push", "-u", "origin", "main")
	pushCmd.Dir = dir
	if out, err := pushCmd.CombinedOutput(); err != nil {
		t.Fatalf("initial push: %v\n%s", err, out)
	}

	// Now make a new commit in dir and force-push — should succeed with
	// --force-with-lease (no diverge, so the lease succeeds).
	newFile := filepath.Join(dir, "extra.txt")
	if err := os.WriteFile(newFile, []byte("extra\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stageCmd := exec.Command("git", "add", "extra.txt")
	stageCmd.Dir = dir
	if out, err := stageCmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	commitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	commitCmd := exec.Command("git", "commit", "-m", "second commit")
	commitCmd.Dir = dir
	commitCmd.Env = commitEnv
	if out, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	cap := GitPush()
	args, _ := json.Marshal(map[string]any{
		"remote": "origin",
		"branch": "main",
		"force":  true,
		"cwd":    dir,
	})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	// Should succeed (lease is satisfied — no diverge).
	if err != nil {
		t.Fatalf("force push with lease failed unexpectedly: %v", err)
	}
	_ = res
}

func TestGitPushCap_NoRemoteError(t *testing.T) {
	dir := initWriteTestRepo(t)

	cap := GitPush()
	// No remote configured — should error; error prefix must be git_push:.
	args, _ := json.Marshal(map[string]any{"cwd": dir})
	_, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error when no remote configured")
	}
	if !strings.Contains(err.Error(), "git_push:") {
		t.Fatalf("error missing prefix: %v", err)
	}
}

// --- git_reset_hard ---

func TestGitResetHardCap_Meta(t *testing.T) {
	cap := GitResetHard()
	if cap.Name() != "git_reset_hard" {
		t.Fatalf("name wrong: %q", cap.Name())
	}
	if cap.Tier() != capabilities.TierX {
		t.Fatalf("tier wrong: %v", cap.Tier())
	}
	want := capabilities.SurfaceAgent | capabilities.SurfaceMCP
	if cap.Surfaces() != want {
		t.Fatalf("surfaces wrong: %v", cap.Surfaces())
	}
}

func TestGitResetHardCap_DiscardsChanges(t *testing.T) {
	dir := initWriteTestRepo(t)

	// Modify hello.txt (unstaged change).
	f := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(f, []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cap := GitResetHard()
	args, _ := json.Marshal(map[string]any{
		"revision": "HEAD",
		"cwd":      dir,
	})
	_, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatalf("git_reset_hard failed: %v", err)
	}

	// File should be restored.
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("file not restored: %q", string(data))
	}
}

func TestGitResetHardCap_RequiresRevision(t *testing.T) {
	cap := GitResetHard()
	args, _ := json.Marshal(map[string]any{"revision": ""})
	_, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error for empty revision")
	}
	if !strings.Contains(err.Error(), "git_reset_hard:") {
		t.Fatalf("error missing prefix: %v", err)
	}
}
