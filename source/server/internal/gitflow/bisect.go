package gitflow

import (
	"context"
	"fmt"
	"regexp"
)

// firstBadRe matches the "first bad commit" line that `git bisect run` prints
// when it converges. Git has emitted two spellings across versions:
//
//	<sha> is the first bad commit
//	<sha> is the first 'bad' commit
//
// The SHA must be a strict lowercase hex object id (7..40 chars, git's
// abbreviation range) at the start of a line.
var firstBadRe = regexp.MustCompile(`(?m)^([0-9a-f]{7,40}) is the first (?:bad|'bad') commit`)

// parseFirstBadCommit extracts the first-bad-commit SHA from `git bisect run`
// output. ok is false when no recognizable first-bad-commit line is present.
func parseFirstBadCommit(out string) (sha string, ok bool) {
	m := firstBadRe.FindStringSubmatch(out)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// BisectRun bisects good..bad running testCommand at each step (exit 0 = good).
// It always resets the bisect state before returning.
func (r *Repo) BisectRun(ctx context.Context, good, bad, testCommand string) (string, error) {
	if _, err := r.run(ctx, "bisect", "start", bad, good); err != nil {
		return "", fmt.Errorf("gitflow: bisect start: %w", err)
	}
	out, runErr := r.run(ctx, "bisect", "run", "sh", "-c", testCommand)
	_, _ = r.run(ctx, "bisect", "reset") // always reset, ignore reset error
	if runErr != nil {
		return "", fmt.Errorf("gitflow: bisect run: %w", runErr)
	}
	sha, ok := parseFirstBadCommit(out)
	if !ok {
		return "", fmt.Errorf("gitflow: bisect: could not identify first bad commit from output:\n%s", out)
	}
	return sha, nil
}
