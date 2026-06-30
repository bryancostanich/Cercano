package gitflow

import (
	"context"
	"testing"
)

func TestAbortInProgressMerge(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	writeFile(t, r, "s.txt", "base")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "base")
	mustRun(t, r, "checkout", "-b", "feature")
	writeFile(t, r, "s.txt", "f")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "f")
	mustRun(t, r, "checkout", "main")
	writeFile(t, r, "s.txt", "m")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "m")
	st, _ := r.Land(ctx, "feature", "main", StrategyRebase)
	if st.Reconciled {
		t.Fatal("precondition: expected conflict/in-progress")
	}
	what, err := r.AbortInProgress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if what != "rebase" {
		t.Fatalf("expected to abort a rebase, got %q", what)
	}
	clean, _ := r.Clean(ctx)
	if !clean {
		t.Fatal("expected clean tree after abort")
	}
}

func TestResetToSafety(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	before, _ := r.RevParse(ctx, "HEAD")
	r.RecordSafety(ctx, "land", "HEAD")
	writeFile(t, r, "x.txt", "x")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "advance")
	if err := r.ResetToSafety(ctx, "land", "main"); err != nil {
		t.Fatal(err)
	}
	now, _ := r.RevParse(ctx, "HEAD")
	if now != before {
		t.Fatalf("expected reset to %s, at %s", before, now)
	}
}
