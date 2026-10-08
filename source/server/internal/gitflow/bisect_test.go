package gitflow

import (
	"context"
	"strings"
	"testing"
)

// TestParseFirstBadCommit is the parser regression table: both spellings git
// emits ("bad" plain and 'bad' quoted), strict hex id requirements, malformed
// ids, and unrelated bisect output must be classified correctly.
func TestParseFirstBadCommit(t *testing.T) {
	const fullSHA = "4a2c0f5f0b0b1e1f1d5ca4cf3e07d1d3b0f8c9d1"

	tests := []struct {
		name  string
		out   string
		want  string
		found bool
	}{
		// Valid formats.
		{
			name:  "plain bad, full 40-char sha",
			out:   "running test\n" + fullSHA + " is the first bad commit\n",
			want:  fullSHA,
			found: true,
		},
		{
			name:  "quoted 'bad', full 40-char sha",
			out:   "running test\n" + fullSHA + " is the first 'bad' commit\n",
			want:  fullSHA,
			found: true,
		},
		{
			name:  "plain bad, 7-char abbreviated sha",
			out:   "4a2c0f5 is the first bad commit",
			want:  "4a2c0f5",
			found: true,
		},
		{
			name:  "quoted 'bad', 7-char abbreviated sha",
			out:   "4a2c0f5 is the first 'bad' commit",
			want:  "4a2c0f5",
			found: true,
		},
		{
			name:  "line must start at beginning of line, not mid-line",
			out:   "prefix " + fullSHA + " is the first bad commit\n",
			want:  "",
			found: false,
		},

		// Malformed ids must be rejected (strict hex, 7..40 lowercase hex chars).
		{name: "too short (6 hex chars)", out: "4a2c0f is the first bad commit", found: false},
		{name: "non-hex characters", out: "notahexstring is the first bad commit", found: false},
		{name: "uppercase hex rejected", out: "4A2C0F5 is the first bad commit", found: false},
		{name: "empty id", out: " is the first bad commit", found: false},
		{
			name:  "truncated sha prefix of a longer hex run (no match inside 50-char run)",
			out:   fullSHA + "abcde is the first bad commit\n",
			found: false,
		},

		// Unrelated output must not match.
		{name: "plain bisect run noise", out: "Bisecting: 0 revisions left to test after this\n", found: false},
		{
			name:  "unrelated commit line",
			out:   fullSHA + " is the first commit of this branch",
			found: false,
		},
		{
			name:  "message body mentioning the phrase without an id line",
			out:   "note: the first bad commit is the one that broke the build\n",
			found: false,
		},
		{
			name:  "indented (subshell-prefixed) line",
			out:   "  " + fullSHA + " is the first bad commit\n",
			found: false,
		},
		{
			name:  "valid line found within larger noisy output",
			out:   "Bisecting: 2 revisions left to test after this\n[exit 1]\n" + fullSHA + " is the first bad commit\nsummary line\n",
			want:  fullSHA,
			found: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseFirstBadCommit(tt.out)
			if ok != tt.found {
				t.Fatalf("parseFirstBadCommit(%q) found=%v, want %v", tt.out, ok, tt.found)
			}
			if ok && got != tt.want {
				t.Fatalf("parseFirstBadCommit(%q) = %q, want %q", tt.out, got, tt.want)
			}
		})
	}
}


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
