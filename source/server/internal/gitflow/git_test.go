package gitflow

import (
	"context"
	"os"
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

func TestCleanIgnoringUntracked_UntrackedFileDoesNotBlock(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// Drop an untracked file in the repo.
	if err := os.WriteFile(r.Dir+"/newfile.txt", []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	if clean, err := r.Clean(ctx); err != nil || clean {
		t.Fatalf("vanilla Clean should now report NOT clean (untracked present): clean=%v err=%v", clean, err)
	}
	clean, err := r.CleanIgnoringUntracked(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !clean {
		t.Fatal("CleanIgnoringUntracked should treat pure-untracked state as clean")
	}
}

func TestCleanIgnoringUntracked_StagedChangeStillBlocks(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// Add and stage a file so status shows it as A (added, index side).
	if err := os.WriteFile(r.Dir+"/staged.txt", []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.run(ctx, "add", "staged.txt"); err != nil {
		t.Fatal(err)
	}
	clean, err := r.CleanIgnoringUntracked(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if clean {
		t.Fatal("CleanIgnoringUntracked should still refuse when a staged change is present")
	}
}

func TestCleanIgnoringUntracked_ModifiedTrackedFileStillBlocks(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// Commit a tracked file, then modify it in the working tree only.
	if err := os.WriteFile(r.Dir+"/tracked.txt", []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.run(ctx, "add", "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.run(ctx, "commit", "-m", "add tracked"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.Dir+"/tracked.txt", []byte("two"), 0644); err != nil {
		t.Fatal(err)
	}
	clean, err := r.CleanIgnoringUntracked(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if clean {
		t.Fatal("CleanIgnoringUntracked should still refuse when a tracked file has unstaged modifications")
	}
}
