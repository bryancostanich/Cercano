package gitflow

import (
	"context"
	"fmt"
	"strings"
)

// CheckpointOptions controls the safety rails used when creating a checkpoint
// commit.
type CheckpointOptions struct {
	// AllowTrunk permits committing on the configured trunk branch. It is meant
	// for explicitly requested current-branch quick fixes, not the default
	// feature-branch workflow.
	AllowTrunk bool

	// Paths, when non-empty, stages only these pathspecs. This is the safe mode
	// for current-branch work because unrelated local edits are left untouched.
	Paths []string
}

// Checkpoint stages all changes and commits them on the current branch with a
// subject (+ optional body). It refuses to commit on trunk, refuses messages
// containing "claude" (case-insensitive), and never adds a Co-Authored-By
// trailer or pushes.
func (r *Repo) Checkpoint(ctx context.Context, subject, body, trunk string) (string, error) {
	return r.CheckpointWithOptions(ctx, subject, body, trunk, CheckpointOptions{})
}

// CheckpointWithOptions commits a solved unit of work with explicit safety
// controls. With Paths set, only those paths are staged and committed; any
// already-staged unrelated files cause the checkpoint to fail rather than being
// swept into the commit.
func (r *Repo) CheckpointWithOptions(ctx context.Context, subject, body, trunk string, opts CheckpointOptions) (string, error) {
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
	if branch == trunk && !opts.AllowTrunk {
		return "", fmt.Errorf("gitflow: checkpoint: refusing to commit on trunk %q; switch to a feature branch or pass allow_trunk with explicit paths", trunk)
	}
	paths := cleanPaths(opts.Paths)
	if opts.AllowTrunk && len(paths) == 0 {
		return "", fmt.Errorf("gitflow: checkpoint: allow_trunk requires explicit paths")
	}
	if len(paths) == 0 {
		if _, err := r.run(ctx, "add", "-A"); err != nil {
			return "", fmt.Errorf("gitflow: checkpoint: stage: %w", err)
		}
	} else {
		if err := r.stageExplicitPaths(ctx, paths); err != nil {
			return "", err
		}
	}
	clean, err := r.Clean(ctx)
	if err != nil {
		return "", fmt.Errorf("gitflow: checkpoint: %w", err)
	}
	if clean {
		return "", fmt.Errorf("gitflow: checkpoint: nothing to commit")
	}
	if len(paths) > 0 {
		staged, err := r.stagedFiles(ctx)
		if err != nil {
			return "", err
		}
		if len(staged) == 0 {
			return "", fmt.Errorf("gitflow: checkpoint: nothing staged for explicit paths")
		}
		if outside := firstOutsidePaths(staged, paths); outside != "" {
			return "", fmt.Errorf("gitflow: checkpoint: refusing to commit already-staged unrelated path %q", outside)
		}
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

func (r *Repo) stageExplicitPaths(ctx context.Context, paths []string) error {
	stagedBefore, err := r.stagedFiles(ctx)
	if err != nil {
		return err
	}
	if outside := firstOutsidePaths(stagedBefore, paths); outside != "" {
		return fmt.Errorf("gitflow: checkpoint: refusing to stage explicit paths while unrelated path %q is already staged", outside)
	}
	args := append([]string{"add", "--"}, paths...)
	if _, err := r.run(ctx, args...); err != nil {
		return fmt.Errorf("gitflow: checkpoint: stage explicit paths: %w", err)
	}
	return nil
}

func (r *Repo) stagedFiles(ctx context.Context) ([]string, error) {
	out, err := r.run(ctx, "diff", "--cached", "--name-only")
	if err != nil {
		return nil, fmt.Errorf("gitflow: checkpoint: list staged files: %w", err)
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

func cleanPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func firstOutsidePaths(files, paths []string) string {
	for _, f := range files {
		if !pathCovered(f, paths) {
			return f
		}
	}
	return ""
}

func pathCovered(file string, paths []string) bool {
	file = strings.TrimSuffix(file, "/")
	for _, p := range paths {
		p = strings.TrimSuffix(p, "/")
		if file == p || strings.HasPrefix(file, p+"/") {
			return true
		}
	}
	return false
}

// hasClaude reports whether s contains "claude" case-insensitively.
func hasClaude(s string) bool { return strings.Contains(strings.ToLower(s), "claude") }
