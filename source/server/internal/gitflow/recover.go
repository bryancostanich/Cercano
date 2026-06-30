package gitflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// AbortInProgress aborts an in-progress rebase or merge, if any.
// Returns "rebase", "merge", or "none" depending on what was aborted.
func (r *Repo) AbortInProgress(ctx context.Context) (string, error) {
	gitDir, err := r.run(ctx, "rev-parse", "--git-dir")
	if err != nil {
		return "", err
	}
	abs := gitDir
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(r.Dir, gitDir)
	}
	if _, statErr := os.Stat(filepath.Join(abs, "rebase-merge")); statErr == nil {
		if _, err := r.run(ctx, "rebase", "--abort"); err != nil {
			return "", err
		}
		return "rebase", nil
	}
	if _, statErr := os.Stat(filepath.Join(abs, "rebase-apply")); statErr == nil {
		if _, err := r.run(ctx, "rebase", "--abort"); err != nil {
			return "", err
		}
		return "rebase", nil
	}
	if _, statErr := os.Stat(filepath.Join(abs, "MERGE_HEAD")); statErr == nil {
		if _, err := r.run(ctx, "merge", "--abort"); err != nil {
			return "", err
		}
		return "merge", nil
	}
	return "none", nil
}

// ResetToSafety checks out branch and hard-resets it to the recorded safety ref.
func (r *Repo) ResetToSafety(ctx context.Context, label, branch string) error {
	sha, err := r.ReadSafety(ctx, label)
	if err != nil {
		return err
	}
	if _, err := r.run(ctx, "checkout", branch); err != nil {
		return fmt.Errorf("gitflow: recover: checkout %q: %w", branch, err)
	}
	if _, err := r.run(ctx, "reset", "--hard", sha); err != nil {
		return fmt.Errorf("gitflow: recover: reset %q to %s: %w", branch, sha, err)
	}
	return nil
}
