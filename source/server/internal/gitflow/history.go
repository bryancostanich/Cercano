package gitflow

import (
	"context"
	"fmt"
	"strings"
)

// SquashToOne collapses all commits on the current branch since its merge-base
// with trunk into a single commit with the given guarded message.
func (r *Repo) SquashToOne(ctx context.Context, trunk, subject, body string) (string, error) {
	if strings.TrimSpace(subject) == "" {
		return "", fmt.Errorf("gitflow: squash: subject is required")
	}
	if hasClaude(subject) || hasClaude(body) {
		return "", fmt.Errorf("gitflow: squash: commit message must not contain \"Claude\"")
	}
	if err := r.RecordSafety(ctx, "history", "HEAD"); err != nil {
		return "", err
	}
	base, err := r.MergeBase(ctx, "HEAD", trunk)
	if err != nil {
		return "", err
	}
	if _, err := r.run(ctx, "reset", "--soft", base); err != nil {
		return "", fmt.Errorf("gitflow: squash: reset --soft %s: %w", base, err)
	}
	args := []string{"commit", "-m", subject}
	if strings.TrimSpace(body) != "" {
		args = append(args, "-m", body)
	}
	if _, err := r.run(ctx, args...); err != nil {
		return "", fmt.Errorf("gitflow: squash: commit: %w", err)
	}
	return r.RevParse(ctx, "HEAD")
}

// Stash saves uncommitted changes (including untracked). Unstash pops them.
func (r *Repo) Stash(ctx context.Context) error {
	_, err := r.run(ctx, "stash", "push", "--include-untracked")
	return err
}

func (r *Repo) Unstash(ctx context.Context) error {
	_, err := r.run(ctx, "stash", "pop")
	return err
}
