// Package gitflow holds deterministic, high-level git workflows. Every function
// shells out to git via exec; there is no model in the control flow and no
// dependency on the capability or dispatch layers. Workflows are exposed to the
// agent through thin capability wrappers in internal/capabilities/builtins.
package gitflow

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Repo is a working directory backed by a git repository.
type Repo struct{ Dir string }

// Open returns a Repo for dir after confirming it is inside a work tree.
func Open(dir string) (*Repo, error) {
	r := &Repo{Dir: dir}
	if out, err := r.run(context.Background(), "rev-parse", "--is-inside-work-tree"); err != nil {
		return nil, fmt.Errorf("gitflow: %s is not a git work tree: %w", dir, err)
	} else if strings.TrimSpace(out) != "true" {
		return nil, fmt.Errorf("gitflow: %s is not a git work tree", dir)
	}
	return r, nil
}

// run executes git with args in the repo dir and returns trimmed combined output.
func (r *Repo) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := strings.TrimSpace(buf.String())
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return out, nil
}

// Clean reports whether the working tree has no staged or unstaged changes.
func (r *Repo) Clean(ctx context.Context) (bool, error) {
	out, err := r.run(ctx, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out == "", nil
}

// CurrentBranch returns the checked-out branch name (or an error in detached HEAD).
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return out, nil
}

// RevParse resolves a ref to a full SHA.
func (r *Repo) RevParse(ctx context.Context, ref string) (string, error) {
	return r.run(ctx, "rev-parse", ref)
}

// IsAncestor reports whether a is an ancestor of b (a..b fast-forwardable).
func (r *Repo) IsAncestor(ctx context.Context, a, b string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", a, b)
	cmd.Dir = r.Dir
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor: %w", err)
}

// MergeBase returns the best common ancestor of a and b.
func (r *Repo) MergeBase(ctx context.Context, a, b string) (string, error) {
	return r.run(ctx, "merge-base", a, b)
}

// CommitsBetween returns the number of commits in from..to (commits on to not on from).
func (r *Repo) CommitsBetween(ctx context.Context, from, to string) (int, error) {
	out, err := r.run(ctx, "rev-list", "--count", from+".."+to)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}
