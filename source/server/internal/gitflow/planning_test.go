package gitflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeDoc writes a planning doc, creating parent directories (efforts/<slug>/
// may not exist yet).
func writeDoc(t *testing.T, r *Repo, name, content string) {
	t.Helper()
	p := filepath.Join(r.Dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCommitPlanningDocs_CommitsOnlyDeclaredPaths verifies the core safety
// property: a planning-docs commit contains exactly the declared spec/plan
// files, while an unrelated STAGED change stays staged (excluded from the
// commit, preserved in the index) and unrelated untracked files stay
// untracked.
func TestCommitPlanningDocs_CommitsOnlyDeclaredPaths(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// Untracked planning docs (the planning-mode state).
	writeDoc(t, r, "efforts/e1/spec.md", "# Spec\n")
	writeDoc(t, r, "efforts/e1/plan.md", "# Plan\n")
	// Unrelated staged change: must be excluded from the commit AND stay staged.
	writeFile(t, r, "staged.txt", "staged content")
	mustRun(t, r, "add", "staged.txt")
	// Unrelated untracked noise: must stay untracked.
	writeFile(t, r, "noise.txt", "noise")

	sha, committed, err := r.CommitPlanningDocs(ctx,
		[]string{"efforts/e1/spec.md", "efforts/e1/plan.md"},
		"checkpoint: planning approval for efforts/e1", "approved spec/plan")
	if err != nil {
		t.Fatal(err)
	}
	if !committed || sha == "" {
		t.Fatalf("committed=%v sha=%q, want a real commit", committed, sha)
	}

	files, err := r.run(ctx, "show", "--name-only", "--pretty=format:", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(files, "efforts/e1/spec.md") || !strings.Contains(files, "efforts/e1/plan.md") {
		t.Fatalf("commit must contain both planning docs; got %q", files)
	}
	if strings.Contains(files, "staged.txt") || strings.Contains(files, "noise.txt") {
		t.Fatalf("commit must not sweep unrelated files; got %q", files)
	}

	// The unrelated staged change survives, still staged and uncommitted.
	staged, err := r.run(ctx, "diff", "--cached", "--name-only")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(staged, "staged.txt") {
		t.Fatalf("unrelated staged change must remain staged after the planning commit; got %q", staged)
	}
	// ...and the untracked noise survives untouched.
	if _, err := os.Stat(filepath.Join(r.Dir, "noise.txt")); err != nil {
		t.Fatalf("unrelated untracked file must be preserved: %v", err)
	}
}

// TestCommitPlanningDocs_CleanDocsAreNoOp: committing already-clean (tracked,
// unmodified) planning docs is an idempotent no-op, not an error.
func TestCommitPlanningDocs_CleanDocsAreNoOp(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	writeDoc(t, r, "efforts/e1/spec.md", "# Spec\n")
	writeDoc(t, r, "efforts/e1/plan.md", "# Plan\n")
	sha1, committed, err := r.CommitPlanningDocs(ctx,
		[]string{"efforts/e1/spec.md", "efforts/e1/plan.md"},
		"checkpoint: planning approval for efforts/e1", "")
	if err != nil || !committed {
		t.Fatalf("first commit: committed=%v err=%v", committed, err)
	}

	sha2, committed, err := r.CommitPlanningDocs(ctx,
		[]string{"efforts/e1/spec.md", "efforts/e1/plan.md"},
		"checkpoint: planning approval for efforts/e1", "")
	if err != nil {
		t.Fatalf("clean retry must not error: %v", err)
	}
	if committed || sha2 != "" {
		t.Fatalf("clean retry must be a no-op; got committed=%v sha=%q", committed, sha2)
	}
	if sha2, _ := r.RevParse(ctx, "HEAD"); sha2 != sha1 {
		t.Fatalf("HEAD moved on no-op retry")
	}
}

// TestCommitPlanningDocs_IgnoredDocsFailNotNoOp: review-probe regression.
// git status's porcelain output HIDES ignored files, so ignored planning docs
// previously looked "tracked and clean" and the checkpoint returned a fake
// no-op. Ignored docs must now fail actionably — never a clean no-op, never
// force-added — and the docs stay on disk for a retry after the ignore rule
// is fixed.
func TestCommitPlanningDocs_IgnoredDocsFailNotNoOp(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	writeFile(t, r, ".gitignore", "efforts/\n")
	mustRun(t, r, "add", ".gitignore")
	mustRun(t, r, "commit", "-m", "add ignore rule")
	writeDoc(t, r, "efforts/e1/spec.md", "# Spec\n")
	writeDoc(t, r, "efforts/e1/plan.md", "# Plan\n")
	headBefore, err := r.RevParse(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	_, committed, err := r.CommitPlanningDocs(ctx,
		[]string{"efforts/e1/spec.md", "efforts/e1/plan.md"},
		"checkpoint: planning approval for efforts/e1", "")
	if err == nil {
		t.Fatal("ignored planning docs must not return a clean no-op; expected an actionable error")
	}
	if committed {
		t.Fatal("ignored docs must not be committed")
	}
	for _, want := range []string{"ignored by .gitignore", "not force-added"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if after, _ := r.RevParse(ctx, "HEAD"); after != headBefore {
		t.Fatalf("HEAD must not move on rejected ignored docs: %s -> %s", headBefore, after)
	}
	// The docs survive on disk, untracked, for a retry after the ignore rule is fixed.
	if _, err := os.Stat(filepath.Join(r.Dir, "efforts", "e1", "plan.md")); err != nil {
		t.Fatalf("planning doc must survive the ignored rejection: %v", err)
	}
}

// TestCommitPlanningDocs_NoOpRequiresTracked: the no-op path requires every
// declared path to be TRACKED and clean. A mix of one clean tracked doc and
// one untracked (not ignored) doc is NOT a no-op — the untracked doc is
// committed now.
func TestCommitPlanningDocs_NoOpRequiresTracked(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	writeDoc(t, r, "efforts/e1/spec.md", "# Spec\n")
	if _, committed, err := r.CommitPlanningDocs(ctx,
		[]string{"efforts/e1/spec.md"},
		"checkpoint: planning approval for efforts/e1", ""); err != nil || !committed {
		t.Fatalf("first commit: committed=%v err=%v", committed, err)
	}
	// spec.md is now tracked and clean; plan.md is new and untracked.
	writeDoc(t, r, "efforts/e1/plan.md", "# Plan\n")

	sha, committed, err := r.CommitPlanningDocs(ctx,
		[]string{"efforts/e1/spec.md", "efforts/e1/plan.md"},
		"checkpoint: planning approval for efforts/e1", "")
	if err != nil {
		t.Fatalf("untracked-but-not-ignored docs must commit, not error: %v", err)
	}
	if !committed || sha == "" {
		t.Fatalf("mixed clean/untracked is not a no-op; committed=%v sha=%q", committed, sha)
	}
	files, err := r.run(ctx, "show", "--name-only", "--pretty=format:", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(files, "spec.md") || !strings.Contains(files, "plan.md") {
		t.Fatalf("only the untracked plan.md should be in the commit; got %q", files)
	}
}

// TestCommitPlanningDocs_CommitFailureReturnsError: a failing commit (here a
// pre-commit hook) surfaces as an error instead of pretending success.
func TestCommitPlanningDocs_CommitFailureReturnsError(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	writeDoc(t, r, "efforts/e1/spec.md", "# Spec\n")
	writeDoc(t, r, "efforts/e1/plan.md", "# Plan\n")

	hook := filepath.Join(r.Dir, ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, committed, err := r.CommitPlanningDocs(ctx,
		[]string{"efforts/e1/spec.md", "efforts/e1/plan.md"},
		"checkpoint: planning approval for efforts/e1", "")
	if err == nil {
		t.Fatal("expected commit failure to surface as an error")
	}
	if committed {
		t.Fatal("committed must be false on failure")
	}
	// The docs stay on disk, uncommitted — a retry can succeed after the fix.
	if _, err := os.Stat(filepath.Join(r.Dir, "efforts", "e1", "spec.md")); err != nil {
		t.Fatalf("planning doc must survive a failed commit: %v", err)
	}
}

// TestCommitPlanningDocs_OnTrunk: planning docs commit on the current branch
// even when it is trunk — planning sessions routinely run on main, and the
// handoff must not strand the docs as untracked there.
func TestCommitPlanningDocs_OnTrunk(t *testing.T) {
	r := newTestRepo(t) // starts on main
	ctx := context.Background()
	writeDoc(t, r, "efforts/e1/spec.md", "# Spec\n")
	writeDoc(t, r, "efforts/e1/plan.md", "# Plan\n")

	sha, committed, err := r.CommitPlanningDocs(ctx,
		[]string{"efforts/e1/spec.md", "efforts/e1/plan.md"},
		"checkpoint: planning approval for efforts/e1", "")
	if err != nil || !committed || sha == "" {
		t.Fatalf("trunk commit: committed=%v sha=%q err=%v", committed, sha, err)
	}
	branch, err := r.CurrentBranch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "main" {
		t.Fatalf("expected to stay on main, got %q", branch)
	}
}

// TestCommitPlanningDocs_Regression_WorktreeLanding pins the original bug at
// the gitflow level: with the docs committed on main, a new worktree that
// changes plan.md lands back onto main with a fast-forward; without the
// commit, the same merge fails on the untracked planning docs.
func TestCommitPlanningDocs_Regression_WorktreeLanding(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	writeDoc(t, r, "efforts/e1/spec.md", "# Spec\n")
	writeDoc(t, r, "efforts/e1/plan.md", "# Plan\n")

	if _, _, err := r.CommitPlanningDocs(ctx,
		[]string{"efforts/e1/spec.md", "efforts/e1/plan.md"},
		"checkpoint: planning approval for efforts/e1", ""); err != nil {
		t.Fatal(err)
	}

	// New worktree off main, change plan.md there, commit.
	wt := filepath.Join(t.TempDir(), "wt")
	cmd := exec.Command("git", "-C", r.Dir, "worktree", "add", "-b", "feat", wt)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(wt, "efforts", "e1", "plan.md"), []byte("# Plan changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wtRepo := &Repo{Dir: wt}
	mustRun(t, wtRepo, "add", "efforts/e1/plan.md")
	mustRun(t, wtRepo, "commit", "-m", "feat: change plan")

	// Fast-forward main: must succeed now that the docs are tracked.
	if out, err := exec.Command("git", "-C", r.Dir, "merge", "--ff-only", "feat").CombinedOutput(); err != nil {
		t.Fatalf("fast-forward main must succeed after the approval commit: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(r.Dir, "efforts", "e1", "plan.md"))
	if err != nil || string(got) != "# Plan changed\n" {
		t.Fatalf("main should carry the worktree's plan.md; got %q err=%v", got, err)
	}
}
