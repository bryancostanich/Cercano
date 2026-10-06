package gitflow

import (
	"context"
	"fmt"
	"strings"

	"cercano/source/server/internal/procx"
)

// CommitPlanningDocs commits exactly the given planning-doc paths on the
// current branch. It exists for the plan-approval handoff: the planning
// artifacts (spec.md / plan.md) must land in git on the branch the session is
// on BEFORE the read-only planning profile exits, otherwise they stay
// untracked on disk and later worktree/merge flows fail rather than
// overwriting them.
//
// Safety rails, deliberately narrower than CheckpointWithOptions:
//   - only the declared paths are committed. The commit uses git's
//     commit-with-pathspec (--only) semantics, so anything else already staged
//     stays staged and untouched, and unstaged/untracked noise is never swept
//     in. No trunk check: planning sessions routinely run on trunk, and the
//     whole point is to checkpoint the two docs wherever the session is.
//   - the no-op path requires every declared path to be TRACKED and clean
//     (already committed, unmodified). Untracked paths are always dirty — they
//     are committed now — and an ignored path is never mistaken for clean:
//     plain porcelain status hides ignored files, so relying on status alone
//     would let ignored docs sail through as a fake no-op. Ignored docs fail
//     with an actionable error instead; they are never force-added.
//   - when every path is tracked and clean, it is a no-op:
//     ("", false, nil).
//
// The subject must be non-empty and must not contain "Claude"
// (case-insensitive), matching the checkpoint conventions.
func (r *Repo) CommitPlanningDocs(ctx context.Context, paths []string, subject, body string) (sha string, committed bool, err error) {
	if strings.TrimSpace(subject) == "" {
		return "", false, fmt.Errorf("gitflow: commit planning docs: subject is required")
	}
	if hasClaude(subject) || hasClaude(body) {
		return "", false, fmt.Errorf("gitflow: commit planning docs: commit message must not contain \"Claude\"")
	}
	clean := cleanPaths(paths)
	if len(clean) == 0 {
		return "", false, fmt.Errorf("gitflow: commit planning docs: no paths given")
	}
	// No-op detection: nothing to do when every declared doc is tracked and
	// does not differ from HEAD. Untracked docs are never a no-op — they are
	// committed now — and ignored docs are refused outright (git status's
	// porcelain output hides ignored files, so they previously looked clean).
	var ignored []string
	tracked, err := r.trackedPaths(ctx, clean)
	if err != nil {
		return "", false, fmt.Errorf("gitflow: commit planning docs: %w", err)
	}
	for _, p := range clean {
		if tracked[p] {
			continue
		}
		isIgn, err := r.isIgnored(ctx, p)
		if err != nil {
			return "", false, fmt.Errorf("gitflow: commit planning docs: %w", err)
		}
		if isIgn {
			ignored = append(ignored, p)
		}
	}
	if len(ignored) > 0 {
		return "", false, fmt.Errorf("gitflow: commit planning docs: %s %s ignored by .gitignore, so the planning checkpoint cannot commit them; planning docs must be tracked — remove the ignore rule (or relocate the docs) and retry. The ignored paths are not force-added", pluralize("path", "paths", len(ignored)), strings.Join(ignored, ", "))
	}
	dirty, err := r.statusForPaths(ctx, clean)
	if err != nil {
		return "", false, fmt.Errorf("gitflow: commit planning docs: %w", err)
	}
	if len(dirty) == 0 {
		// Every declared path is tracked and clean: an idempotent no-op.
		return "", false, nil
	}
	// Stage the declared docs (required so commit's pathspec can pick up
	// untracked files). This touches the index entries for these paths only.
	args := append([]string{"add", "--"}, clean...)
	if _, err := r.run(ctx, args...); err != nil {
		return "", false, fmt.Errorf("gitflow: commit planning docs: stage: %w", err)
	}
	// Commit ONLY the declared pathspecs. git's default with a pathspec is
	// --only: the commit takes the working-tree contents of these paths and
	// disregards anything staged for other paths, which remain staged after
	// the commit.
	commitArgs := []string{"commit", "-m", subject}
	if strings.TrimSpace(body) != "" {
		commitArgs = append(commitArgs, "-m", body)
	}
	commitArgs = append(commitArgs, "--")
	commitArgs = append(commitArgs, clean...)
	if _, err := r.runFor(ctx, gitWriteTimeout, commitArgs...); err != nil {
		return "", false, fmt.Errorf("gitflow: commit planning docs: commit: %w", err)
	}
	sha, err = r.RevParse(ctx, "HEAD")
	if err != nil {
		return "", false, fmt.Errorf("gitflow: commit planning docs: rev-parse HEAD: %w", err)
	}
	return sha, true, nil
}

// statusForPaths returns the porcelain status lines that touch the given
// pathspecs (staged, unstaged, or untracked). An empty result means every path
// is tracked and clean.
func (r *Repo) statusForPaths(ctx context.Context, paths []string) ([]string, error) {
	args := append([]string{"status", "--porcelain", "--"}, paths...)
	out, err := r.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// trackedPaths returns the subset of paths git currently tracks (ls-files
// honors the pathspec, so this is a cheap targeted query). Paths missing from
// the map are untracked — possibly brand new, possibly ignored.
func (r *Repo) trackedPaths(ctx context.Context, paths []string) (map[string]bool, error) {
	args := append([]string{"ls-files", "--"}, paths...)
	out, err := r.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	tracked := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if p := strings.TrimSpace(line); p != "" {
			tracked[p] = true
		}
	}
	return tracked, nil
}

// isIgnored reports whether path matches a .gitignore rule. git check-ignore
// exits 0 for ignored paths and 1 for paths that are not ignored; other exit
// codes (or a failure to run git at all) are errors. A non-zero exit is not an
// error here, so this shells procx directly instead of going through run().
func (r *Repo) isIgnored(ctx context.Context, path string) (bool, error) {
	res, err := procx.Run(ctx, procx.Options{
		Args:    []string{"git", "check-ignore", "--", path},
		Dir:     r.Dir,
		Timeout: gitQueryTimeout,
	})
	if err != nil {
		return false, fmt.Errorf("git check-ignore %s: %w: %s", path, err, strings.TrimSpace(string(res.Combined())))
	}
	switch res.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("git check-ignore %s: exit status %d: %s", path, res.ExitCode, strings.TrimSpace(string(res.Combined())))
	}
}

// pluralize picks the singular or plural noun for n.
func pluralize(singular, plural string, n int) string {
	if n == 1 {
		return singular
	}
	return plural
}
