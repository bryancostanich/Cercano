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
	if clean, err := r.Clean(ctx); err != nil {
		return st, err
	} else if !clean {
		return st, fmt.Errorf("gitflow: land: working tree not clean — commit or checkpoint first")
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
func (r *Repo) Finalize(ctx context.Context, feature, trunk string) error {
	if _, err := r.run(ctx, "checkout", trunk); err != nil {
		return fmt.Errorf("gitflow: finalize: checkout %q: %w", trunk, err)
	}
	if _, err := r.run(ctx, "merge", "--ff-only", feature); err != nil {
		return fmt.Errorf("gitflow: finalize: ff-only merge of %q into %q failed (reconcile first): %w", feature, trunk, err)
	}
	return nil
}
