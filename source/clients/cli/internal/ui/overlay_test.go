package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestComposeOverlay_EmptyBoxReturnsBaseUnchanged(t *testing.T) {
	base := "hello\nworld"
	got := composeOverlay(base, "", 2, 0)
	if got != base {
		t.Fatalf("empty box mutated base: %q", got)
	}
}

func TestComposeOverlay_SimpleASCIIAtOrigin(t *testing.T) {
	base := "1234567890\nabcdefghij\nABCDEFGHIJ"
	box := "XX\nYY"
	got := composeOverlay(base, box, 3, 1)
	// Compare visible content only — the compositor brackets the overlay
	// with SGR resets, which are invisible but present in the raw output.
	want := "1234567890\nabcXXfghij\nABCYYFGHIJ"
	if ansi.Strip(got) != want {
		t.Fatalf("splice wrong:\n got visible: %q\nwant visible: %q", ansi.Strip(got), want)
	}
}

func TestComposeOverlay_OverlayPastEndPadsLine(t *testing.T) {
	base := "abc\n\ndef" // middle line is empty
	box := "XX"
	got := composeOverlay(base, box, 5, 1)
	// Middle line: pad 5 spaces then "XX".
	lines := strings.Split(got, "\n")
	if len(lines) != 3 || lines[0] != "abc" || lines[2] != "def" {
		t.Fatalf("bg lines mutated: %q", lines)
	}
	visible := ansi.Strip(lines[1])
	if visible != "     XX" {
		t.Fatalf("middle line visible = %q, want %q", visible, "     XX")
	}
}

func TestComposeOverlay_OverlayExtendsPastBaseIsPreserved(t *testing.T) {
	// The compositor treats the overlay as authoritative — if it extends past
	// bg's end, the extension is kept (callers are responsible for picking
	// x/y so the modal fits the terminal frame).
	base := "abcdefgh" // width 8
	box := "XXXXXX"    // width 6
	got := composeOverlay(base, box, 4, 0)
	visible := ansi.Strip(got)
	if visible != "abcdXXXXXX" {
		t.Fatalf("splice wrong, visible = %q, want %q", visible, "abcdXXXXXX")
	}
}

func TestComposeOverlay_NegativeYIsNoop(t *testing.T) {
	base := "abc\ndef"
	box := "XX"
	got := composeOverlay(base, box, 0, -1)
	if got != base {
		t.Fatalf("negative y mutated base: %q", got)
	}
}

func TestComposeOverlay_PastEndOfBaseIsNoop(t *testing.T) {
	base := "abc\ndef"
	box := "XX"
	got := composeOverlay(base, box, 0, 5)
	if got != base {
		t.Fatalf("y past base mutated it: %q", got)
	}
}

func TestComposeOverlay_PreservesANSIStylingOnRight(t *testing.T) {
	// A styled base line where the tail after our overlay carries red color.
	// After splicing we should see the tail still rendered in red — meaning
	// the styling escape isn't lost.
	base := "AAA\x1b[31mBBBCCC\x1b[0m" // AAA plain, BBBCCC red
	box := "X"
	got := composeOverlay(base, box, 3, 0) // overwrite first B
	// The result must still contain red-open + the styled tail.
	if !strings.Contains(got, "\x1b[31m") {
		t.Fatalf("red open sequence dropped: %q", got)
	}
	// Visible layout: AAA + X (overwrites first B) + BBCCC = 9 cells.
	stripped := ansi.Strip(got)
	if stripped != "AAAXBBCCC" {
		t.Fatalf("stripped visible = %q, want AAAXBBCCC", stripped)
	}
}

func TestComposeOverlay_ResetSGRIsolatesOverlay(t *testing.T) {
	base := "\x1b[31mAAAA\x1b[0m"
	// Overlay carries green; make sure it doesn't leak into the base tail
	// (though in this case the tail is empty). Guards the reset contract.
	box := "\x1b[32mXX\x1b[0m"
	got := composeOverlay(base, box, 1, 0)
	if !strings.Contains(got, resetSGR+"\x1b[32mXX"+resetSGR) {
		t.Fatalf("overlay not bracketed by resets: %q", got)
	}
}

func TestComposeOverlay_MultiLineWithMixedRowLengths(t *testing.T) {
	// A base with lines of different widths; overlay should splice cleanly
	// even when a row is shorter than x.
	base := "the quick brown fox\nover\nthe lazy dog"
	box := "[XXX]\n[YYY]"
	got := composeOverlay(base, box, 2, 0)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("line count changed: %d", len(lines))
	}
	if lines[2] != "the lazy dog" {
		t.Fatalf("untouched row 2 mutated: %q", lines[2])
	}
	// Row 0: at col 2 → "th[XXX]uick brown fox"
	if !strings.HasPrefix(ansi.Strip(lines[0]), "th[XXX]") {
		t.Fatalf("row 0 splice wrong: %q", ansi.Strip(lines[0]))
	}
	// Row 1: bg was "over" width 4, we start at col 2; expect "ov[YYY]"
	if ansi.Strip(lines[1]) != "ov[YYY]" {
		t.Fatalf("row 1 splice wrong: %q", ansi.Strip(lines[1]))
	}
}
