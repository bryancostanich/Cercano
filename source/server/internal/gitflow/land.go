package gitflow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Divergence summarizes how a feature branch relates to trunk.
type Divergence struct {
	TrunkAhead      int  // commits on trunk not on feature
	FeatureAhead    int  // commits on feature not on trunk
	FastForwardable bool // trunk is already an ancestor of feature
}

// Divergence reports the commit gap between feature and trunk.
func (r *Repo) Divergence(ctx context.Context, feature, trunk string) (Divergence, error) {
	var d Divergence
	base, err := r.MergeBase(ctx, feature, trunk)
	if err != nil {
		return d, err
	}
	if d.TrunkAhead, err = r.CommitsBetween(ctx, base, trunk); err != nil {
		return d, err
	}
	if d.FeatureAhead, err = r.CommitsBetween(ctx, base, feature); err != nil {
		return d, err
	}
	if d.FastForwardable, err = r.IsAncestor(ctx, trunk, feature); err != nil {
		return d, err
	}
	return d, nil
}

// Strategy selects whether Land reconciles via rebase or merge.
type Strategy string

const (
	StrategyRebase Strategy = "rebase"
	StrategyMerge  Strategy = "merge"
)

// LandState is the result of a Land (or LandContinue) call.
type LandState struct {
	Reconciled bool
	Conflicts  []string
	Strategy   Strategy
	// Dir is the worktree the reconcile actually ran in. Differs from the
	// calling Repo's Dir when the feature branch lives in a linked worktree;
	// callers must run the test gate and signal reads there.
	Dir string
}

// ConflictedFiles returns the currently unmerged paths (git diff --name-only --diff-filter=U).
// It is the exported equivalent of conflictedFiles, for use by capability wrappers.
func (r *Repo) ConflictedFiles(ctx context.Context) ([]string, error) {
	return r.conflictedFiles(ctx)
}

// conflictedFiles returns the unmerged paths (git diff --name-only --diff-filter=U).
func (r *Repo) conflictedFiles(ctx context.Context) ([]string, error) {
	out, err := r.run(ctx, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(strings.TrimSpace(out), "\n"), nil
}

// Land reconciles feature against trunk on the feature branch. On conflict it
// pauses (leaves the repo mid-rebase/merge) and returns the conflicted files.
func (r *Repo) Land(ctx context.Context, feature, trunk string, strategy Strategy) (LandState, error) {
	st := LandState{Strategy: strategy}
	// Worktree-first topology: when the feature branch is checked out in a
	// DIFFERENT worktree, run the whole reconcile there — a checkout here
	// would fail ("already used by worktree"), and the branch's files live
	// there anyway. One level of recursion: the retargeted repo resolves the
	// branch to itself and proceeds.
	if wt, wtErr := r.WorktreeFor(ctx, feature); wtErr == nil && wt != "" && !sameDir(wt, r.Dir) {
		return (&Repo{Dir: wt}).Land(ctx, feature, trunk, strategy)
	}
	st.Dir = r.Dir
	// Untracked files ride along undisturbed through fast-forward merges
	// and rebases — refusing on their presence turns unrelated worktree
	// directories into a landing blocker. Only staged/modified changes
	// to tracked files can collide.
	if clean, err := r.CleanIgnoringUntracked(ctx); err != nil {
		return st, err
	} else if !clean {
		return st, fmt.Errorf("gitflow: land: working tree has staged or unstaged changes — commit, checkpoint, or stash first")
	}
	if err := r.RecordSafety(ctx, "land", trunk); err != nil {
		return st, err
	}
	if _, err := r.run(ctx, "checkout", feature); err != nil {
		return st, fmt.Errorf("gitflow: land: checkout %q: %w", feature, err)
	}
	var op string
	switch strategy {
	case StrategyMerge:
		op = "merge"
	default:
		op = "rebase"
		st.Strategy = StrategyRebase
	}
	if _, err := r.run(ctx, op, trunk); err != nil {
		// Distinguish a conflict (expected) from a hard failure.
		conf, cErr := r.conflictedFiles(ctx)
		if cErr == nil && len(conf) > 0 {
			st.Conflicts = conf
			return st, nil
		}
		return st, fmt.Errorf("gitflow: land: %s %q: %w", op, trunk, err)
	}
	st.Reconciled = true
	return st, nil
}

// fileHasConflictMarkers reports whether the working-tree file still contains
// git conflict markers (i.e. an unresolved conflict). A file deleted as part of
// resolution is treated as resolved.
func (r *Repo) fileHasConflictMarkers(path string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(r.Dir, path))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("gitflow: scan conflict markers %s: %w", path, err)
	}
	s := string(data)
	return strings.HasPrefix(s, "<<<<<<< ") || strings.Contains(s, "\n<<<<<<< ") ||
		strings.Contains(s, "\n>>>>>>> "), nil
}

// LandContinue resumes a paused reconcile after the caller resolved conflicts.
//
// It must NOT stage blindly: `git add -A` would clear the unmerged index
// entries regardless of whether the content is actually resolved, so a file
// still containing conflict markers would be staged and committed by
// --continue. Instead it reads the still-unmerged file set (which remains
// unmerged in the index until staged, even after the working tree is edited)
// and refuses to proceed while any of those files still contain conflict
// markers — that is the genuine "still paused" guard. Only once every conflict
// file is marker-free does it stage and continue.
func (r *Repo) LandContinue(ctx context.Context, strategy Strategy) (LandState, error) {
	st := LandState{Strategy: strategy}
	// The paused reconcile lives where Land ran it, which under the
	// worktree-first topology is the feature's linked worktree — not
	// necessarily the caller's directory. Retarget before reading state.
	if wt, wtErr := r.WorktreeMidReconcile(ctx); wtErr == nil && wt != "" && !sameDir(wt, r.Dir) {
		return (&Repo{Dir: wt}).LandContinue(ctx, strategy)
	}
	st.Dir = r.Dir
	conf, err := r.conflictedFiles(ctx)
	if err != nil {
		return st, err
	}
	var unresolved []string
	for _, f := range conf {
		marked, err := r.fileHasConflictMarkers(f)
		if err != nil {
			return st, err
		}
		if marked {
			unresolved = append(unresolved, f)
		}
	}
	if len(unresolved) > 0 {
		st.Conflicts = unresolved
		return st, nil // caller must still resolve these files
	}
	if _, err := r.run(ctx, "add", "-A"); err != nil {
		return st, fmt.Errorf("gitflow: land --continue: stage: %w", err)
	}
	op := "rebase"
	if strategy == StrategyMerge {
		op = "merge"
	}
	// GIT_EDITOR=true avoids an interactive editor on merge/rebase --continue.
	cmd := exec.CommandContext(ctx, "git", op, "--continue")
	cmd.Dir = r.Dir
	cmd.Env = append(cmd.Environ(), "GIT_EDITOR=true")
	if out, err := cmd.CombinedOutput(); err != nil {
		conf, _ := r.conflictedFiles(ctx)
		if len(conf) > 0 {
			st.Conflicts = conf
			return st, nil
		}
		return st, fmt.Errorf("gitflow: land --continue: %s: %w: %s", op, err, strings.TrimSpace(string(out)))
	}
	st.Reconciled = true
	return st, nil
}

// RunTests runs testCommand via `sh -c` in the repo dir; non-zero exit is an error.
func (r *Repo) RunTests(ctx context.Context, testCommand string) (string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", testCommand)
	cmd.Dir = r.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("gitflow: test gate failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Finalize fast-forwards trunk to feature. Errors if not fast-forwardable.
// The fast-forward runs in whichever worktree holds trunk — under the
// worktree-first topology that is typically the root workspace while this
// Repo points at the feature's worktree, and a checkout here would fail.
func (r *Repo) Finalize(ctx context.Context, feature, trunk string) error {
	if wt, wtErr := r.WorktreeFor(ctx, trunk); wtErr == nil && wt != "" && !sameDir(wt, r.Dir) {
		tr := &Repo{Dir: wt}
		if clean, cErr := tr.CleanIgnoringUntracked(ctx); cErr != nil {
			return cErr
		} else if !clean {
			return fmt.Errorf("gitflow: finalize: trunk worktree %s has staged or unstaged changes — commit or stash there first", wt)
		}
		if _, err := tr.run(ctx, "merge", "--ff-only", feature); err != nil {
			return fmt.Errorf("gitflow: finalize: ff-only merge of %q into %q failed (reconcile first): %w", feature, trunk, err)
		}
		return nil
	}
	if _, err := r.run(ctx, "checkout", trunk); err != nil {
		return fmt.Errorf("gitflow: finalize: checkout %q: %w", trunk, err)
	}
	if _, err := r.run(ctx, "merge", "--ff-only", feature); err != nil {
		return fmt.Errorf("gitflow: finalize: ff-only merge of %q into %q failed (reconcile first): %w", feature, trunk, err)
	}
	return nil
}

// ResolutionSignals holds deterministic risk signals computed from a resolved
// conflict file set and repo config.
type ResolutionSignals struct {
	Files         []string
	HandEdited    int
	SensitiveHits []string
	Diff          string
}

func matchesAny(globs []string, path string) bool {
	for _, g := range globs {
		if ok, _ := filepath.Match(g, path); ok {
			return true
		}
		// also match against the basename so "*.pb.go" matches "api/x.pb.go"
		if ok, _ := filepath.Match(g, filepath.Base(path)); ok {
			return true
		}
	}
	return false
}

// ResolutionSignalsFor computes deterministic risk signals from the conflicted
// file set and config. Generated files (matched by a Regen glob) are not counted
// as hand-edited. Pure: no model, no mutation.
func (r *Repo) ResolutionSignalsFor(ctx context.Context, files []string, cfg Config) ResolutionSignals {
	sig := ResolutionSignals{Files: files}
	var regenGlobs []string
	for g := range cfg.Regen {
		regenGlobs = append(regenGlobs, g)
	}
	for _, f := range files {
		if !matchesAny(regenGlobs, f) {
			sig.HandEdited++
		}
		if matchesAny(cfg.SensitivePaths, f) {
			sig.SensitiveHits = append(sig.SensitiveHits, f)
		}
	}
	// Diff is intentionally NOT computed here: by the time the resolved file set
	// is known the reconcile may already be committed, so `git diff HEAD` would
	// be empty. Callers set sig.Diff from the resolution range via DiffRange.
	return sig
}

// DiffRange returns `git diff from..to`, capped at 16 KiB — used to feed the
// actual resolution into the review seam.
func (r *Repo) DiffRange(ctx context.Context, from, to string) (string, error) {
	d, err := r.run(ctx, "diff", from+".."+to)
	if err != nil {
		return "", err
	}
	const maxDiff = 16 * 1024
	if len(d) > maxDiff {
		d = d[:maxDiff] + "\n… (diff truncated)"
	}
	return d, nil
}
