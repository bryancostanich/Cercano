package gitflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// worktreeRig builds the worktree-first landing topology: trunk ("main")
// checked out in the root workspace, "feature" in a linked worktree with one
// commit of its own, and trunk advanced past the branch point.
func worktreeRig(t *testing.T) (root *Repo, feat *Repo, wtDir string) {
	t.Helper()
	root = newTestRepo(t)
	ctx := context.Background()

	writeFile(t, root, "base.txt", "base")
	mustRun(t, root, "add", "-A")
	mustRun(t, root, "commit", "-m", "chore: base")

	wtDir = filepath.Join(t.TempDir(), "feature-wt")
	mustRun(t, root, "worktree", "add", "-b", "feature", wtDir)
	feat = &Repo{Dir: wtDir}
	writeFile(t, feat, "feat.txt", "f")
	mustRun(t, feat, "add", "-A")
	mustRun(t, feat, "commit", "-m", "feat: work")

	writeFile(t, root, "trunk.txt", "m")
	mustRun(t, root, "add", "-A")
	mustRun(t, root, "commit", "-m", "chore: trunk moves")
	_ = ctx
	return root, feat, wtDir
}

// TestLandAcrossWorktrees pins the split topology end to end: Land invoked
// from the ROOT workspace reconciles in the feature's linked worktree (the
// old code died on `git checkout feature` — "already used by worktree"), and
// Finalize invoked from the FEATURE worktree fast-forwards trunk in the root
// workspace (the old code died on `git checkout main`).
func TestLandAcrossWorktrees(t *testing.T) {
	root, feat, wtDir := worktreeRig(t)
	ctx := context.Background()

	st, err := root.Land(ctx, "feature", "main", StrategyRebase)
	if err != nil {
		t.Fatalf("Land from root: %v", err)
	}
	if !st.Reconciled || len(st.Conflicts) != 0 {
		t.Fatalf("expected clean reconcile, got %+v", st)
	}
	if !sameDir(st.Dir, wtDir) {
		t.Errorf("reconcile ran in %s, want the feature worktree %s", st.Dir, wtDir)
	}

	if err := feat.Finalize(ctx, "feature", "main"); err != nil {
		t.Fatalf("Finalize from feature worktree: %v", err)
	}
	out, err := root.run(ctx, "rev-parse", "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	shas := strings.Fields(out)
	if len(shas) != 2 || shas[0] != shas[1] {
		t.Errorf("main not fast-forwarded to feature: %v", shas)
	}
	head, _ := root.run(ctx, "rev-parse", "HEAD")
	if head != shas[0] {
		t.Errorf("root workspace HEAD %s not advanced with main %s", head, shas[0])
	}
}

// TestLandContinueRetargetsMidReconcileWorktree pins the continue path: a
// conflicted land pauses in the feature worktree; LandContinue invoked from
// the ROOT repo finds the mid-rebase worktree and resumes there.
func TestLandContinueRetargetsMidReconcileWorktree(t *testing.T) {
	root, feat, wtDir := worktreeRig(t)
	ctx := context.Background()

	// Manufacture a conflict: both sides edit base.txt.
	writeFile(t, root, "base.txt", "trunk edit")
	mustRun(t, root, "add", "-A")
	mustRun(t, root, "commit", "-m", "chore: trunk edits base")
	writeFile(t, feat, "base.txt", "feature edit")
	mustRun(t, feat, "add", "-A")
	mustRun(t, feat, "commit", "-m", "feat: feature edits base")

	st, err := root.Land(ctx, "feature", "main", StrategyRebase)
	if err != nil {
		t.Fatalf("Land: %v", err)
	}
	if len(st.Conflicts) == 0 {
		t.Fatal("expected a conflict pause")
	}
	if !sameDir(st.Dir, wtDir) {
		t.Errorf("conflict paused in %s, want feature worktree %s", st.Dir, wtDir)
	}

	// Resolve IN THE WORKTREE, then continue FROM THE ROOT.
	if err := os.WriteFile(filepath.Join(wtDir, "base.txt"), []byte("resolved"), 0o644); err != nil {
		t.Fatal(err)
	}
	st2, err := root.LandContinue(ctx, StrategyRebase)
	if err != nil {
		t.Fatalf("LandContinue from root: %v", err)
	}
	if !st2.Reconciled {
		t.Fatalf("expected reconciled after continue, got %+v", st2)
	}
	if !sameDir(st2.Dir, wtDir) {
		t.Errorf("continue ran in %s, want feature worktree %s", st2.Dir, wtDir)
	}
}

// TestFinalizeRefusesDirtyTrunkWorktree pins the safety guard: the trunk
// worktree fast-forward refuses when that worktree has local changes.
func TestFinalizeRefusesDirtyTrunkWorktree(t *testing.T) {
	root, feat, _ := worktreeRig(t)
	ctx := context.Background()

	if _, err := root.Land(ctx, "feature", "main", StrategyRebase); err != nil {
		t.Fatalf("Land: %v", err)
	}
	// Dirty the ROOT (trunk) workspace with a tracked-file edit.
	writeFile(t, root, "base.txt", "uncommitted local change")

	err := feat.Finalize(ctx, "feature", "main")
	if err == nil {
		t.Fatal("expected Finalize to refuse a dirty trunk worktree")
	}
	if !strings.Contains(err.Error(), "staged or unstaged changes") {
		t.Errorf("error should name the dirty-trunk cause, got: %v", err)
	}
}
