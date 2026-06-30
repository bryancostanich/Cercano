package gitflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, r *Repo, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.Dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointCommitsOnFeatureBranch(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.run(ctx, "checkout", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, r, "a.txt", "hello")
	sha, err := r.Checkpoint(ctx, "feat: add a", "Adds the a file.", "main")
	if err != nil {
		t.Fatal(err)
	}
	if sha == "" {
		t.Fatal("expected a commit sha")
	}
	msg, _ := r.run(ctx, "log", "-1", "--pretty=%B")
	if !strings.Contains(msg, "feat: add a") || !strings.Contains(msg, "Adds the a file.") {
		t.Fatalf("message not assembled: %q", msg)
	}
}

func TestCheckpointRejectsClaudeAndTrunkAndEmpty(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	writeFile(t, r, "b.txt", "x")
	// On trunk → reject.
	if _, err := r.Checkpoint(ctx, "feat: b", "", "main"); err == nil {
		t.Fatal("expected error committing on trunk")
	}
	// Move to a branch.
	if _, err := r.run(ctx, "checkout", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	// "claude" anywhere → reject (case-insensitive).
	if _, err := r.Checkpoint(ctx, "feat: b (Claude wrote this)", "", "main"); err == nil {
		t.Fatal("expected error rejecting 'claude' in subject")
	}
	// Empty subject → reject.
	if _, err := r.Checkpoint(ctx, "", "body", "main"); err == nil {
		t.Fatal("expected error on empty subject")
	}
}
