package builtins

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
)

// site_mesh_job.rs in the reproduction was 32,826 bytes against a 32,768-byte
// cap: 58 bytes over, 0.18% of the file. The worker read it seven times.
const reproFileBytes = 32826

func readFile(t *testing.T, dir, args string) *capabilities.Result {
	t.Helper()
	res, err := ReadFile().Execute(context.Background(), &capabilities.Call{
		Args: []byte(args), WorkDir: dir,
	})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	return res
}

func writeLines(t *testing.T, dir, name string, n int, line string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A file barely over the cap loses a sliver of content but is told to "refine",
// which is the instruction that drove repeated re-reads.
func TestSlightOverflowTruncatesAndInvitesReread(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("x", reproFileBytes)
	if err := os.WriteFile(filepath.Join(dir, "big.rs"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	res := readFile(t, dir, `{"path":"big.rs"}`)
	over := reproFileBytes - agenttools.MaxResultBytes

	t.Logf("file=%dB cap=%dB over=%dB (%.2f%%) truncated=%v note=%q",
		reproFileBytes, agenttools.MaxResultBytes, over,
		100*float64(over)/reproFileBytes, res.Truncated, res.Note)

	if !res.Truncated {
		t.Fatal("expected truncation at 58 bytes over the cap")
	}
	if !strings.Contains(res.Note, "refine to get more") {
		t.Fatalf("note = %q, want the refine instruction", res.Note)
	}
	// The note names no concrete next call: no byte offset, no line number.
	if strings.ContainsAny(res.Note, "0123456789") && !strings.Contains(res.Note, "32") {
		t.Logf("note contains a number other than the cap: %q", res.Note)
	}
}

// The cap is applied to the sliced text, so an explicit line range is truncated
// too. Asking for a narrower range is not guaranteed to escape the cap.
func TestLineRangeIsAlsoTruncated(t *testing.T) {
	dir := t.TempDir()
	// 700 lines of 100 bytes = ~70 KB, well over the cap.
	writeLines(t, dir, "wide.rs", 700, strings.Repeat("y", 99))

	res := readFile(t, dir, `{"path":"wide.rs","start":100,"end":600}`)
	t.Logf("requested lines 100-600, got %dB truncated=%v note=%q",
		len(res.Text), res.Truncated, res.Note)

	if !res.Truncated {
		t.Fatal("expected an explicit line range to be truncated as well")
	}
	if got := strings.Count(res.Text, "\n"); got >= 500 {
		t.Fatalf("got %d lines of the 500 requested; expected fewer", got)
	}
}

// A range that fits is returned whole: the cap is the only limit, so narrowing
// does work, the model just is not told how far to narrow.
func TestNarrowRangeReturnsRequestedLines(t *testing.T) {
	dir := t.TempDir()
	writeLines(t, dir, "wide.rs", 700, strings.Repeat("y", 99))

	res := readFile(t, dir, `{"path":"wide.rs","start":100,"end":200}`)
	if res.Truncated {
		t.Fatalf("101 lines of 100B should fit under %dB", agenttools.MaxResultBytes)
	}
	// Lines 100-200 inclusive is 101 lines; selectLines does not add a trailing
	// newline, so the requested span yields 100 newline separators.
	if got := strings.Count(res.Text, "\n"); got != 100 {
		t.Fatalf("got %d newlines, want 100 (101 lines)", got)
	}
}
