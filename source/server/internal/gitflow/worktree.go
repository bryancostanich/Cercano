package gitflow

import (
	"context"
	"fmt"
)

// CreateWorktree creates branch off trunk and adds a linked worktree at path.
func (r *Repo) CreateWorktree(ctx context.Context, path, branch, trunk string) (string, error) {
	if _, err := r.run(ctx, "worktree", "add", "-b", branch, path, trunk); err != nil {
		return "", fmt.Errorf("gitflow: create worktree %q (branch %q off %q): %w", path, branch, trunk, err)
	}
	return path, nil
}
