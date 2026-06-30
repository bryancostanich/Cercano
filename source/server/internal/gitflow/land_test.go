package gitflow

import (
	"context"
	"testing"
)

func mustRun(t *testing.T, r *Repo, args ...string) {
	t.Helper()
	if _, err := r.run(context.Background(), args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func TestDivergence(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// feature branches off main, both advance.
	mustRun(t, r, "checkout", "-b", "feature")
	writeFile(t, r, "f1.txt", "1")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "feat: f1")
	mustRun(t, r, "checkout", "main")
	writeFile(t, r, "m1.txt", "1")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "chore: m1")

	d, err := r.Divergence(ctx, "feature", "main")
	if err != nil {
		t.Fatal(err)
	}
	if d.TrunkAhead != 1 || d.FeatureAhead != 1 || d.FastForwardable {
		t.Fatalf("divergence: %+v", d)
	}
}
