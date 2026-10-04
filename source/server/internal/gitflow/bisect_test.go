package gitflow

import (
	"context"
	"strings"
	"testing"
)

func TestBisectRunFindsBadCommit(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// good commit: marker file says "good"
	writeFile(t, r, "marker.txt", "good")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "good")
	good, _ := r.RevParse(ctx, "HEAD")
	writeFile(t, r, "pad.txt", "1")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "pad1")
	// bad commit: marker flips to "bad"
	writeFile(t, r, "marker.txt", "bad")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "break")
	bad, _ := r.RevParse(ctx, "HEAD")
	writeFile(t, r, "pad2.txt", "2")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "pad2")

	// test command: exit 0 while marker says good, 1 once it says bad.
	sha, err := r.BisectRun(ctx, good, "HEAD", `test "$(cat marker.txt)" = good`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(bad, sha) && sha != bad {
		t.Fatalf("expected first-bad %s, got %s", bad, sha)
	}
}

// Git versions differ in whether they quote the bisect term. These are the
// two output forms observed on macOS and the Linux CI runner.
func TestBisectOutputAcrossGitVersions(t *testing.T) {
	const sha = "2d102457a3362010f62f53c9c7cb31c39aa16f8c"
	for _, line := range []string{sha + " is the first bad commit", sha + " is the first 'bad' commit"} {
		output := "running 'sh' '-c' 'test command'\n" + line + "\ncommit " + sha + "\n    break\n"
		match := firstBadRe.FindStringSubmatch(output)
		if len(match) != 2 || match[1] != sha {
			t.Fatalf("did not recognize Git result: %q", line)
		}
	}
	if firstBadRe.MatchString("There are only skipped commits left to test.") {
		t.Fatal("ambiguous bisect treated as a result")
	}
}
