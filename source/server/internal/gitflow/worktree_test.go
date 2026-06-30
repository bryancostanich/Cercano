package gitflow

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCreateWorktree(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	wt := filepath.Join(t.TempDir(), "feature-x")
	got, err := r.CreateWorktree(ctx, wt, "feature-x", "main")
	if err != nil {
		t.Fatal(err)
	}
	if got != wt {
		t.Fatalf("path: got %q want %q", got, wt)
	}
	// The worktree is a valid repo on the new branch.
	wr, err := Open(wt)
	if err != nil {
		t.Fatal(err)
	}
	if br, _ := wr.CurrentBranch(ctx); br != "feature-x" {
		t.Fatalf("worktree branch: %q", br)
	}
	// Creating the same branch again fails.
	if _, err := r.CreateWorktree(ctx, filepath.Join(t.TempDir(), "dup"), "feature-x", "main"); err == nil {
		t.Fatal("expected error creating an existing branch")
	}
}
