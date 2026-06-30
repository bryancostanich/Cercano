package gitflow

import (
	"context"
	"os/exec"
	"testing"
)

// newTestRepo creates a temp git repo with one commit on branch "main".
func newTestRepo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	run("commit", "--allow-empty", "-m", "root")
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRepoBasics(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	clean, err := r.Clean(ctx)
	if err != nil || !clean {
		t.Fatalf("expected clean tree: clean=%v err=%v", clean, err)
	}
	br, err := r.CurrentBranch(ctx)
	if err != nil || br != "main" {
		t.Fatalf("branch: %q err=%v", br, err)
	}
	if _, err := r.RevParse(ctx, "HEAD"); err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	anc, err := r.IsAncestor(ctx, "HEAD", "HEAD")
	if err != nil || !anc {
		t.Fatalf("HEAD should be ancestor of itself: %v %v", anc, err)
	}
}
