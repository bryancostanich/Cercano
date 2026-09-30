// Package gitflow holds deterministic, high-level git workflows. Every function
// shells out to git via exec; there is no model in the control flow and no
// dependency on the capability or dispatch layers. Workflows are exposed to the
// agent through thin capability wrappers in internal/capabilities/builtins.
package gitflow

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"cercano/source/server/internal/procx"
)

// Timeouts for shelling out to git. Plain porcelain/plumbing queries are
// local and near-instant, so a small bound catches a wedged git (an index
// lock held by another process, a credential prompt waiting on a tty that
// will never answer) instead of hanging the turn. Network-capable and
// history-rewriting operations get a much larger bound: they can legitimately
// take minutes on a big repo, and killing one midway is worse than waiting.
const (
	gitQueryTimeout = 30 * time.Second
	gitWriteTimeout = 10 * time.Minute
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

// run executes git with args in the repo dir and returns trimmed combined
// output, bounded by gitQueryTimeout. Use runFor when the operation can
// legitimately run long.
func (r *Repo) run(ctx context.Context, args ...string) (string, error) {
	return r.runFor(ctx, gitQueryTimeout, args...)
}

// runFor is run with an explicit timeout.
func (r *Repo) runFor(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	res, err := procx.Run(ctx, procx.Options{
		Args:    append([]string{"git"}, args...),
		Dir:     r.Dir,
		Timeout: timeout,
	})
	out := strings.TrimSpace(string(res.Combined()))
	if err != nil {
		// Timeout/cancel: out still holds whatever git managed to print,
		// which is usually the only clue about where it wedged.
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	if res.ExitCode != 0 {
		return out, fmt.Errorf("git %s: exit status %d: %s", strings.Join(args, " "), res.ExitCode, out)
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

// CleanIgnoringUntracked reports whether the working tree has no staged or
// unstaged changes to tracked files. Untracked entries (porcelain "?? ...")
// don't count.
//
// Rationale: fast-forward merges and rebases only fail on untracked files
// when the operation would write to that exact path; otherwise untracked
// files ride along undisturbed. Refusing to land whenever any untracked
// file exists is over-strict — it turns "an unrelated session created a
// worktree directory next to yours" into a landing blocker. If a real
// collision does happen, the underlying merge/rebase reports it clearly
// and the caller sees the same error they would have seen post-check.
func (r *Repo) CleanIgnoringUntracked(ctx context.Context) (bool, error) {
	out, err := r.run(ctx, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if out == "" {
		return true, nil
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "?? ") {
			return false, nil
		}
	}
	return true, nil
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
	// Exit 1 is the legitimate "no" answer here, not a failure — procx
	// reports a clean non-zero exit with a nil error for exactly this case.
	res, err := procx.Run(ctx, procx.Options{
		Args:    []string{"git", "merge-base", "--is-ancestor", a, b},
		Dir:     r.Dir,
		Timeout: gitQueryTimeout,
	})
	if err != nil {
		return false, fmt.Errorf("git merge-base --is-ancestor: %w", err)
	}
	switch res.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("git merge-base --is-ancestor: exit status %d: %s",
			res.ExitCode, strings.TrimSpace(string(res.Combined())))
	}
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
