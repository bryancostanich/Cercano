package gitflow

import (
	"context"
	"fmt"
)

func safetyRefName(label string) string { return "refs/cercano/safety/" + label }

// RecordSafety stores the SHA of rev under a stable per-label ref so a later
// Recover can reset back to it ("undo the last <label> workflow").
func (r *Repo) RecordSafety(ctx context.Context, label, rev string) error {
	sha, err := r.RevParse(ctx, rev)
	if err != nil {
		return fmt.Errorf("gitflow: record safety %q: %w", label, err)
	}
	if _, err := r.run(ctx, "update-ref", safetyRefName(label), sha); err != nil {
		return fmt.Errorf("gitflow: record safety %q: %w", label, err)
	}
	return nil
}

// ReadSafety returns the SHA recorded under label, or an error if none exists.
func (r *Repo) ReadSafety(ctx context.Context, label string) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--verify", "--quiet", safetyRefName(label))
	if err != nil || out == "" {
		return "", fmt.Errorf("gitflow: no safety ref for %q", label)
	}
	return out, nil
}
