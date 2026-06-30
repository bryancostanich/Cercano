package gitflow

import (
	"context"
	"fmt"
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
