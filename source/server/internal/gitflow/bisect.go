package gitflow

import (
	"context"
	"fmt"
	"regexp"
)

var firstBadRe = regexp.MustCompile(`(?m)^([0-9a-f]{7,40}) is the first bad commit`)

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
	m := firstBadRe.FindStringSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("gitflow: bisect: could not identify first bad commit from output:\n%s", out)
	}
	return m[1], nil
}
