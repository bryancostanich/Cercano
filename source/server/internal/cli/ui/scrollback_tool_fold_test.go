package ui

import (
	"strings"
	"testing"
)

// A folded tool entry must be a single line. Tool summaries can carry newlines
// (e.g. the Bash result "$ cmd\n[exit=1, elapsed=...]"), and an embedded newline
// leaked a second, un-indented, truncated fragment into scrollback.
func TestRenderToolEntryFoldedSingleLine(t *testing.T) {
	e := ToolEntry{
		ToolName:      "Bash",
		ArgsSummary:   "find /x\n-type d",
		ResultSummary: "$ find /x -type d\n[exit=1, elaps",
		Status:        ToolStatusComplete,
		Folded:        true,
	}
	got := renderToolEntry(e, 80, false)
	if strings.Contains(got, "\n") {
		t.Fatalf("folded tool entry must be one line; got embedded newline:\n%q", got)
	}
}
