package builtins

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The old note, "showed first 32 KiB; refine to get more", named no next call.
// A narrowed range could hit the same cap and return the identical message, so
// the model had to guess how far to narrow. These tests pin a note that always
// names a concrete continuation.

var continuation = regexp.MustCompile(`showed lines (\d+)-(\d+) of (\d+); request start=(\d+) for the remainder`)

func parseNote(t *testing.T, note string) (from, to, total, next int) {
	t.Helper()
	m := continuation.FindStringSubmatch(note)
	if m == nil {
		t.Fatalf("note %q does not name a concrete continuation", note)
	}
	from, _ = strconv.Atoi(m[1])
	to, _ = strconv.Atoi(m[2])
	total, _ = strconv.Atoi(m[3])
	next, _ = strconv.Atoi(m[4])
	return
}

func TestWholeFileTruncationNamesNextStart(t *testing.T) {
	dir := t.TempDir()
	writeLines(t, dir, "big.rs", 1200, strings.Repeat("z", 99))

	res := readFile(t, dir, `{"path":"big.rs"}`)
	if !res.Truncated {
		t.Fatal("expected truncation")
	}
	from, to, total, next := parseNote(t, res.Note)
	t.Logf("note=%q", res.Note)

	if from != 1 {
		t.Fatalf("from = %d, want 1", from)
	}
	if total != 1200 {
		t.Fatalf("total = %d, want 1200", total)
	}
	if to >= total {
		t.Fatalf("to = %d must be short of total %d", to, total)
	}
	// The final shown line may be cut mid-line, so continuation must re-read it
	// rather than skip it.
	if next != to {
		t.Fatalf("next = %d, want %d so the partial last line is re-read", next, to)
	}
}

// Continuation must be expressed in absolute file line numbers, so following it
// makes progress instead of restarting.
func TestRangeTruncationContinuesFromAbsoluteLine(t *testing.T) {
	dir := t.TempDir()
	writeLines(t, dir, "big.rs", 1200, strings.Repeat("z", 99))

	res := readFile(t, dir, `{"path":"big.rs","start":400,"end":1200}`)
	if !res.Truncated {
		t.Fatal("expected a truncated range")
	}
	from, to, total, next := parseNote(t, res.Note)
	t.Logf("note=%q", res.Note)

	if from != 400 {
		t.Fatalf("from = %d, want the requested start 400", from)
	}
	if total != 1200 {
		t.Fatalf("total = %d, want the file total 1200", total)
	}
	if next <= 400 {
		t.Fatalf("next = %d would not advance past start 400", next)
	}
	if next != to {
		t.Fatalf("next = %d, want %d", next, to)
	}
}

// Following the note must terminate: each hop advances and the final read is
// not truncated. This is the property the old note lacked.
func TestFollowingContinuationTerminates(t *testing.T) {
	dir := t.TempDir()
	writeLines(t, dir, "big.rs", 3000, strings.Repeat("z", 99))

	start, hops, prev := 1, 0, 0
	for {
		res := readFile(t, dir, `{"path":"big.rs","start":`+strconv.Itoa(start)+`}`)
		hops++
		if !res.Truncated {
			break
		}
		_, _, _, next := parseNote(t, res.Note)
		if next <= prev {
			t.Fatalf("hop %d did not advance: next=%d prev=%d", hops, next, prev)
		}
		prev, start = next, next
		if hops > 20 {
			t.Fatal("continuation did not terminate")
		}
	}
	t.Logf("whole file consumed in %d reads", hops)
}

func TestUntruncatedReadHasNoNote(t *testing.T) {
	dir := t.TempDir()
	writeLines(t, dir, "small.rs", 10, "fn main() {}")

	res := readFile(t, dir, `{"path":"small.rs"}`)
	if res.Truncated || res.Note != "" {
		t.Fatalf("small file: truncated=%v note=%q", res.Truncated, res.Note)
	}
}
