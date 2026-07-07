package ui

import (
	"strings"
	"testing"
)

func safeRune(s []rune, i int) string {
	if i < len(s) {
		return string(s[i])
	}
	return "<eol>"
}

// Regression: the collapse rail must run unbroken down a multiline / wrapped
// tool body — including the args line. The bug was that lines built as
// faint.Render("    args: …") wrapped the indent INSIDE the ANSI style, so the
// rail overlay (which replaces the plain space at byte offset 2) no-op'd and the
// pipe broke on that line while the plain-prefixed result lines kept it.
func TestRail_ContinuousDownMultilineBody(t *testing.T) {
	c := newTestChatView(70, 40)
	c.SetEntriesSlice([]*Entry{
		{Tool: &ToolEntry{
			ToolName:    "Bash",
			ArgsSummary: "cat x",
			FullArgs:    `{"cmd":["cat","x/banner.go"]}`,
			FullResult:  "$ cat x/banner.go\n[exit=1]\n\nstderr:\ncat: no such file or directory that is quite long and will wrap across the viewport width\n\n(exit 1)",
			Status:      ToolStatusError,
			Folded:      false,
		}},
		{Tool: &ToolEntry{ToolName: "Bash", ArgsSummary: "find", Status: ToolStatusComplete, Folded: true}},
	})
	c.groupExpanded[0] = true
	c.SetEntries(c.Entries())
	c.SetYOffset(0)

	lines := c.PlainLines()
	rail := map[rune]bool{'▾': true, '│': true, '╰': true, '▸': true}

	// Bound the group block: from the summary ▾ (col 4) to the last line whose
	// col-4 glyph is the closing ╰.
	start := -1
	for i, l := range lines {
		s := []rune(stripAnsiCSI(l))
		if len(s) > 4 && s[4] == '▾' {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("no expanded group header found:\n%s", strings.Join(lines, "\n"))
	}
	end := start
	for i := len(lines) - 1; i > start; i-- {
		s := []rune(stripAnsiCSI(lines[i]))
		if len(s) > 4 && s[4] == '╰' {
			end = i
			break
		}
	}

	sawArgs := false
	for i := start; i <= end; i++ {
		s := []rune(stripAnsiCSI(lines[i]))
		if len(s) <= 4 || !rail[s[4]] {
			t.Errorf("rail broken at block line %d (col 4 = %q): %q", i, safeRune(s, 4), string(s))
		}
		if strings.Contains(string(s), "args:") {
			sawArgs = true
		}
	}
	if !sawArgs {
		t.Error("expected an args: line within the block")
	}
}
