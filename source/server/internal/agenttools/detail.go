package agenttools

import (
	"fmt"
	"strings"
)

// Result.Detail conventions: a clean, timing-free, content-free outcome token
// per tool ("480 lines", "12 matches", "+3 −1"), surfaced by the CLI next to the
// status glyph. These helpers keep the grammar and the edit-delta math in one
// place so every tool formats consistently.

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

// editDetail describes a single-span replacement as "+added −removed", where the
// removed block is the old text and the added block is the new text. The minus
// is a U+2212 to match the rest of the UI.
func editDetail(oldText, newText string) string {
	return fmt.Sprintf("+%d −%d", lineCount(newText), lineCount(oldText))
}
