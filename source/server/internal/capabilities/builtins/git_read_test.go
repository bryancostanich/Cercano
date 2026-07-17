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

// initTestRepo creates a temporary git repository with one commit and returns
// its path. The commit creates a single file so git_log has something to show.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
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

	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")

	f := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(f, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "hello.txt")
	run("commit", "-m", "initial commit")

	return dir
}

// --- git_status ---

func TestGitStatusCap_Meta(t *testing.T) {
	cap := GitStatus()
	if cap.Name() != "git_status" {
		t.Fatalf("name wrong: %q", cap.Name())
	}
	if cap.Tier() != capabilities.TierR {
		t.Fatalf("tier wrong: %q", cap.Tier())
	}
	want := capabilities.SurfaceAgent | capabilities.SurfaceMCP
	if cap.Surfaces() != want {
		t.Fatalf("surfaces wrong: %v", cap.Surfaces())
	}
}

func TestGitStatusCap_CleanRepo(t *testing.T) {
	dir := initTestRepo(t)
	cap := GitStatus()
	args, _ := json.Marshal(map[string]any{"path": dir})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows in clean repo, got %d", len(res.Rows))
	}
	if res.Detail != "0 changes" {
		t.Fatalf("detail wrong: %q", res.Detail)
	}
}

func TestGitStatusCap_ModifiedFile(t *testing.T) {
	dir := initTestRepo(t)

	// Modify the file — should appear as unstaged modification.
	f := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(f, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cap := GitStatus()
	args, _ := json.Marshal(map[string]any{"path": dir})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	row := res.Rows[0]
	if row["path"] != "hello.txt" {
		t.Fatalf("path wrong: %q", row["path"])
	}
	if !strings.Contains(row["status"].(string), "modified") {
		t.Fatalf("status wrong: %q", row["status"])
	}
}

func TestGitStatusCap_UntrackedFile(t *testing.T) {
	dir := initTestRepo(t)

	// Add an untracked file.
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cap := GitStatus()
	args, _ := json.Marshal(map[string]any{"path": dir})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	row := res.Rows[0]
	if row["status"] != "untracked" {
		t.Fatalf("status wrong: %q", row["status"])
	}
}

func TestGitStatusCap_ErrorPrefix(t *testing.T) {
	cap := GitStatus()
	// Point at a non-git dir — should error with correct prefix.
	args, _ := json.Marshal(map[string]any{"path": t.TempDir()})
	_, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error for non-git dir")
	}
	if !strings.Contains(err.Error(), "git_status:") {
		t.Fatalf("error missing prefix: %v", err)
	}
}

// --- git_log ---

func TestGitLogCap_Meta(t *testing.T) {
	cap := GitLog()
	if cap.Name() != "git_log" {
		t.Fatalf("name wrong: %q", cap.Name())
	}
	if cap.Tier() != capabilities.TierR {
		t.Fatalf("tier wrong: %q", cap.Tier())
	}
	want := capabilities.SurfaceAgent | capabilities.SurfaceMCP
	if cap.Surfaces() != want {
		t.Fatalf("surfaces wrong: %v", cap.Surfaces())
	}
}

func TestGitLogCap_OneCommit(t *testing.T) {
	dir := initTestRepo(t)
	cap := GitLog()
	args, _ := json.Marshal(map[string]any{"path": dir})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 commit row, got %d", len(res.Rows))
	}
	row := res.Rows[0]
	for _, field := range []string{"sha", "author", "date", "subject"} {
		if _, ok := row[field]; !ok {
			t.Fatalf("missing field %q in row", field)
		}
	}
	if row["subject"] != "initial commit" {
		t.Fatalf("subject wrong: %q", row["subject"])
	}
	if res.Detail != "1 commit" {
		t.Fatalf("detail wrong: %q", res.Detail)
	}
}

func TestGitLogCap_LimitArg(t *testing.T) {
	dir := initTestRepo(t)

	// Add a second commit.
	run := func(args ...string) {
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
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "b.txt")
	run("commit", "-m", "second commit")

	// limit=1 should return only the most recent commit.
	cap := GitLog()
	args, _ := json.Marshal(map[string]any{"path": dir, "limit": 1})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row with limit=1, got %d", len(res.Rows))
	}
	if res.Rows[0]["subject"] != "second commit" {
		t.Fatalf("expected newest commit first, got %q", res.Rows[0]["subject"])
	}
}

func TestGitLogCap_NoCommitsError(t *testing.T) {
	// Empty repo — git log returns no commits.
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	cap := GitLog()
	args, _ := json.Marshal(map[string]any{"path": dir})
	_, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error for repo with no commits")
	}
	if !strings.Contains(err.Error(), "git_log:") {
		t.Fatalf("error missing prefix: %v", err)
	}
}

// --- git_info ---

func TestGitInfoCap_Meta(t *testing.T) {
	cap := GitInfo()
	if cap.Name() != "git_info" {
		t.Fatalf("name wrong: %q", cap.Name())
	}
	if cap.Tier() != capabilities.TierR {
		t.Fatalf("tier wrong: %q", cap.Tier())
	}
	want := capabilities.SurfaceAgent | capabilities.SurfaceMCP
	if cap.Surfaces() != want {
		t.Fatalf("surfaces wrong: %v", cap.Surfaces())
	}
}

func TestGitInfoCap_ReportsBranchAndHead(t *testing.T) {
	dir := initTestRepo(t)
	cap := GitInfo()
	args, _ := json.Marshal(map[string]any{"path": dir})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(res.JSON, &got); err != nil {
		t.Fatal(err)
	}
	if got["branch"] != "main" {
		t.Fatalf("branch = %v, want main", got["branch"])
	}
	if got["head"] == "" {
		t.Fatalf("head should be populated: %#v", got)
	}
	wantRoot, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got["root"] != wantRoot {
		t.Fatalf("root = %v, want %s", got["root"], wantRoot)
	}
	if !strings.Contains(res.Detail, "main") {
		t.Fatalf("detail = %q, want branch summary", res.Detail)
	}
}
