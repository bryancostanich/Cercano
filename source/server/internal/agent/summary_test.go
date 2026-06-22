package agent

import (
	"strings"
	"testing"
	"unicode/utf8"

	"cercano/source/server/internal/agenttools"
)

// The folded tool summary should prefer the curated one-line Note over the raw
// result text (which for Bash is "$ cmd\n\n[exit=...]..." — useless as a glance).
func TestSummarizeResultPrefersNote(t *testing.T) {
	got := summarizeResult(&agenttools.Result{
		Type: agenttools.ResultText,
		Text: "$ find /x\n\n[exit=1, elapsed=2ms]\n",
		Note: "exit 1 · 2ms",
	})
	if got != "exit 1 · 2ms" {
		t.Fatalf("got %q, want the Note", got)
	}
}

// With no Note, fall back to the first line of the text, truncated rune-safely
// (the old t[:80] sliced bytes and could split a multibyte rune).
func TestSummarizeResultFirstLineRuneSafe(t *testing.T) {
	line := strings.Repeat("é", 100) // 100 two-byte runes
	got := summarizeResult(&agenttools.Result{Type: agenttools.ResultText, Text: line + "\nsecond line"})
	if !utf8.ValidString(got) {
		t.Fatalf("summary is not valid UTF-8: %q", got)
	}
	if r := []rune(got); len(r) != 81 || r[80] != '…' {
		t.Fatalf("want 80 runes + ellipsis, got %d runes: %q", len(r), got)
	}
	if strings.Contains(got, "second") {
		t.Fatalf("summary should be the first line only: %q", got)
	}
}

// Short single-line text passes through unchanged.
func TestSummarizeResultShortText(t *testing.T) {
	if got := summarizeResult(&agenttools.Result{Type: agenttools.ResultText, Text: "staged 3 paths"}); got != "staged 3 paths" {
		t.Fatalf("got %q", got)
	}
}
