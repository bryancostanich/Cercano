package gitflow

import (
	"context"
	"fmt"
	"strings"
)

// Checkpoint stages all changes and commits them on the current branch with a
// subject (+ optional body). It refuses to commit on trunk, refuses messages
// containing "claude" (case-insensitive), and never adds a Co-Authored-By
// trailer or pushes.
func (r *Repo) Checkpoint(ctx context.Context, subject, body, trunk string) (string, error) {
	if strings.TrimSpace(subject) == "" {
		return "", fmt.Errorf("gitflow: checkpoint: subject is required")
	}
	if hasClaude(subject) || hasClaude(body) {
		return "", fmt.Errorf("gitflow: checkpoint: commit message must not contain \"Claude\"")
	}
	branch, err := r.CurrentBranch(ctx)
	if err != nil {
		return "", fmt.Errorf("gitflow: checkpoint: %w", err)
	}
	if branch == trunk {
		return "", fmt.Errorf("gitflow: checkpoint: refusing to commit on trunk %q; switch to a feature branch", trunk)
	}
	if _, err := r.run(ctx, "add", "-A"); err != nil {
		return "", fmt.Errorf("gitflow: checkpoint: stage: %w", err)
	}
	clean, err := r.Clean(ctx)
	if err != nil {
		return "", fmt.Errorf("gitflow: checkpoint: %w", err)
	}
	if clean {
		return "", fmt.Errorf("gitflow: checkpoint: nothing to commit")
	}
	args := []string{"commit", "-m", subject}
	if strings.TrimSpace(body) != "" {
		args = append(args, "-m", body)
	}
	if _, err := r.run(ctx, args...); err != nil {
		return "", fmt.Errorf("gitflow: checkpoint: commit: %w", err)
	}
	return r.RevParse(ctx, "HEAD")
}

// hasClaude reports whether s contains "claude" case-insensitively.
func hasClaude(s string) bool { return strings.Contains(strings.ToLower(s), "claude") }
