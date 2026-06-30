package gitflow

import (
	"context"
	"testing"
)

func mustRun(t *testing.T, r *Repo, args ...string) {
	t.Helper()
	if _, err := r.run(context.Background(), args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func TestDivergence(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// feature branches off main, both advance.
	mustRun(t, r, "checkout", "-b", "feature")
	writeFile(t, r, "f1.txt", "1")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "feat: f1")
	mustRun(t, r, "checkout", "main")
	writeFile(t, r, "m1.txt", "1")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "chore: m1")

	d, err := r.Divergence(ctx, "feature", "main")
	if err != nil {
		t.Fatal(err)
	}
	if d.TrunkAhead != 1 || d.FeatureAhead != 1 || d.FastForwardable {
		t.Fatalf("divergence: %+v", d)
	}
}

func TestLandRebaseClean(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustRun(t, r, "checkout", "-b", "feature")
	writeFile(t, r, "f.txt", "feature")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "feat: f")
	mustRun(t, r, "checkout", "main")
	writeFile(t, r, "m.txt", "main")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "chore: m")

	st, err := r.Land(ctx, "feature", "main", StrategyRebase)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Reconciled || len(st.Conflicts) != 0 {
		t.Fatalf("expected clean reconcile, got %+v", st)
	}
	// feature now contains main's commit (ff-able onto main).
	ff, _ := r.IsAncestor(ctx, "main", "feature")
	if !ff {
		t.Fatal("feature should fast-forward onto main after rebase")
	}
}

func TestLandRebaseConflictPauses(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	writeFile(t, r, "shared.txt", "base")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "chore: base")
	mustRun(t, r, "checkout", "-b", "feature")
	writeFile(t, r, "shared.txt", "feature-change")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "feat: f")
	mustRun(t, r, "checkout", "main")
	writeFile(t, r, "shared.txt", "main-change")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "chore: m")

	st, err := r.Land(ctx, "feature", "main", StrategyRebase)
	if err != nil {
		t.Fatal(err)
	}
	if st.Reconciled || len(st.Conflicts) == 0 {
		t.Fatalf("expected conflict pause, got %+v", st)
	}
	if st.Conflicts[0] != "shared.txt" {
		t.Fatalf("expected shared.txt conflict, got %v", st.Conflicts)
	}
}

func TestLandContinueAndFinalize(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	writeFile(t, r, "shared.txt", "base")
	mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "chore: base")
	mustRun(t, r, "checkout", "-b", "feature")
	writeFile(t, r, "shared.txt", "feature-change")
	mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "feat: f")
	mustRun(t, r, "checkout", "main")
	writeFile(t, r, "shared.txt", "main-change")
	mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "chore: m")

	st, _ := r.Land(ctx, "feature", "main", StrategyRebase)
	if st.Reconciled {
		t.Fatal("precondition: expected conflict")
	}
	// Resolve the conflict (simulate the agent editing the file).
	writeFile(t, r, "shared.txt", "resolved")
	st2, err := r.LandContinue(ctx, StrategyRebase)
	if err != nil {
		t.Fatal(err)
	}
	if !st2.Reconciled {
		t.Fatalf("expected reconciled after continue, got %+v", st2)
	}
	// Test gate passes.
	if _, err := r.RunTests(ctx, "true"); err != nil {
		t.Fatalf("test gate: %v", err)
	}
	// Finalize: main fast-forwards to feature.
	if err := r.Finalize(ctx, "feature", "main"); err != nil {
		t.Fatal(err)
	}
	ff, _ := r.IsAncestor(ctx, "feature", "main")
	if !ff {
		t.Fatal("main should contain feature after finalize")
	}
}

func TestRunTestsRed(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.RunTests(context.Background(), "false"); err == nil {
		t.Fatal("expected RunTests to error on a failing command")
	}
}
