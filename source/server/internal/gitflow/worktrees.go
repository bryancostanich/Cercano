package gitflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// WorktreeFor returns the absolute path of the worktree (root or linked) that
// has branch checked out, or "" when the branch is not checked out anywhere.
// Under the worktree-first protocol a land routinely spans two worktrees —
// the feature in a linked worktree, trunk in the root workspace — and git
// refuses to check out a branch that another worktree holds, so operations
// must run where their branch already lives.
func (r *Repo) WorktreeFor(ctx context.Context, branch string) (string, error) {
	out, err := r.run(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	want := "branch refs/heads/" + branch
	dir := ""
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			dir = strings.TrimPrefix(line, "worktree ")
		case line == want:
			return dir, nil
		}
	}
	return "", nil
}

// WorktreeMidReconcile returns the path of the worktree with a rebase or
// merge in progress, or "" when none. The continue path needs it: a paused
// reconcile lives where Land ran it (the feature's worktree, where HEAD is
// detached mid-rebase so WorktreeFor can't find it), not necessarily the
// caller's working directory.
func (r *Repo) WorktreeMidReconcile(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		dir := strings.TrimPrefix(line, "worktree ")
		wr := &Repo{Dir: dir}
		for _, marker := range []string{"rebase-merge", "rebase-apply", "MERGE_HEAD"} {
			p, perr := wr.run(ctx, "rev-parse", "--git-path", marker)
			if perr != nil {
				continue
			}
			if !filepath.IsAbs(p) {
				p = filepath.Join(dir, p)
			}
			if _, serr := os.Stat(p); serr == nil {
				return dir, nil
			}
		}
	}
	return "", nil
}

// sameDir reports whether two paths name the same directory, tolerating
// symlink aliases (macOS TMPDIR: /var/… vs /private/var/…).
func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return false
	}
	return ra == rb
}
