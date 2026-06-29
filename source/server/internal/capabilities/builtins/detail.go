package builtins

import (
	"fmt"
	"io/fs"
	"strings"
)

// Result.Detail conventions: a clean, timing-free, content-free outcome token
// per capability ("480 lines", "12 matches"), surfaced by the CLI next to the
// status glyph. These helpers keep the grammar in one place.

// countLabel renders a count with the right grammatical number: "1 match",
// "3 matches".
func countLabel(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// lineCount returns how many lines a string spans: 0 for empty, otherwise one
// more than the number of interior newlines.
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// isUTF8Boundary reports whether b is a UTF-8 sequence boundary byte.
// Continuation bytes (10xxxxxx, 0x80..0xBF) are not boundaries.
func isUTF8Boundary(b byte) bool {
	return (b & 0xC0) != 0x80
}

// looksBinary heuristic: NUL byte in the first 8 KiB. Catches most binaries
// without trying to enumerate every extension.
func looksBinary(b []byte) bool {
	n := len(b)
	if n > 8192 {
		n = 8192
	}
	for i := 0; i < n; i++ {
		if b[i] == 0 {
			return true
		}
	}
	return false
}

// selectLines returns the lines [start, end] (1-indexed, inclusive).
func selectLines(text string, start, end int) string {
	lines := strings.Split(text, "\n")
	if start < 1 {
		start = 1
	}
	if end < 1 || end > len(lines) {
		end = len(lines)
	}
	if start > end || start > len(lines) {
		return ""
	}
	return strings.Join(lines[start-1:end], "\n")
}

// isSymlink reports whether a FileMode represents a symbolic link.
func isSymlink(m fs.FileMode) bool { return m&fs.ModeSymlink != 0 }
