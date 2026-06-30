package gitflow

import (
	"context"
	"strings"
	"testing"
)

func TestSquashToOne(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustRun(t, r, "checkout", "-b", "feature")
	writeFile(t, r, "a.txt", "1")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "wip 1")
	writeFile(t, r, "b.txt", "2")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "wip 2")

	sha, err := r.SquashToOne(ctx, "main", "feat: the feature", "Body here.")
	if err != nil {
		t.Fatal(err)
	}
	if sha == "" {
		t.Fatal("expected a commit")
	}
	// Exactly one commit on feature since main.
	n, _ := r.CommitsBetween(ctx, "main", "feature")
	if n != 1 {
		t.Fatalf("expected 1 commit after squash, got %d", n)
	}
	msg, _ := r.run(ctx, "log", "-1", "--pretty=%B")
	if !strings.Contains(msg, "feat: the feature") {
		t.Fatalf("squashed message wrong: %q", msg)
	}
}

func TestSquashRejectsClaude(t *testing.T) {
	r := newTestRepo(t)
	mustRun(t, r, "checkout", "-b", "feature")
	writeFile(t, r, "a.txt", "1")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "wip")
	if _, err := r.SquashToOne(context.Background(), "main", "feat: by Claude", ""); err == nil {
		t.Fatal("expected rejection of 'claude' in squash message")
	}
}

func TestSquashRefusesOnTrunk(t *testing.T) {
	r := newTestRepo(t) // checked out on main (the trunk)
	writeFile(t, r, "a.txt", "1")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "wip")
	if _, err := r.SquashToOne(context.Background(), "main", "feat: x", ""); err == nil {
		t.Fatal("expected refusal to squash on the trunk branch")
	}
}
