package ui

import (
	"strings"
	"testing"
	"time"

	"cercano/source/clients/cli/internal/theme"
)

// findPlainLine returns the index of the first content line containing
// substr, failing the test when absent.
func findPlainLine(t *testing.T, c *chatView, substr string) int {
	t.Helper()
	for i, ln := range c.PlainLines() {
		if strings.Contains(stripAnsiCSI(ln), substr) {
			return i
		}
	}
	t.Fatalf("no content line contains %q; content:\n%s", substr, strings.Join(c.PlainLines(), "\n"))
	return -1
}

func toolClickEntries() []*Entry {
	return []*Entry{
		{Role: RoleUser, Content: "do stuff"},
		{Tool: &ToolEntry{ToolName: "Read", ArgsSummary: "a.go", FullArgs: `{"path":"a.go"}`, Status: ToolStatusComplete, Duration: 5 * time.Millisecond, Folded: true}},
		{Tool: &ToolEntry{ToolName: "Read", ArgsSummary: "b.go", FullArgs: `{"path":"b.go"}`, Status: ToolStatusComplete, Duration: 7 * time.Millisecond, Folded: true}},
		{Role: RoleAssistant, Content: "prose after the tools"},
	}
}

// A click on prose below a tool group must NOT be claimed as a fold toggle.
// MouseToggleFold returning true short-circuits the host's selection begin —
// over-claiming is exactly the bug that killed text selection (every click
// below the first tool line in scrollback was swallowed as a toggle).
func TestMouseToggleFold_ProseBelowGroupFallsThrough(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 40)
	c.SetEntriesSlice(toolClickEntries())
	c.rebuild()
	line := findPlainLine(t, &c, "prose after the tools")
	if c.MouseToggleFold(10, line) {
		t.Fatalf("click on prose line %d was claimed as a fold toggle", line)
	}
}

// Prose that happens to contain the arrow glyph must not become clickable —
// the old implementation sniffed rendered lines for "▸ " and misdirected
// clicks when ordinary content contained it.
func TestMouseToggleFold_ArrowGlyphInProseFallsThrough(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 40)
	entries := append(toolClickEntries(), &Entry{Role: RoleAssistant, Content: "▸ looks like an arrow but is prose"})
	c.SetEntriesSlice(entries)
	c.rebuild()
	line := findPlainLine(t, &c, "looks like an arrow")
	if c.MouseToggleFold(2, line) {
		t.Fatalf("click on glyph-bearing prose line %d was claimed as a fold toggle", line)
	}
}

// A click on a collapsed group's summary arrow row expands the group.
func TestMouseToggleFold_SummaryArrowExpandsGroup(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 40)
	c.SetEntriesSlice(toolClickEntries())
	c.rebuild()
	line := findPlainLine(t, &c, "2 tool calls")
	if !c.MouseToggleFold(2, line) {
		t.Fatalf("click on summary arrow row %d was not claimed", line)
	}
	if !c.groupExpanded[1] {
		t.Fatal("summary arrow click did not expand the group (groupExpanded[1] unset)")
	}
}

// In an expanded group, a per-entry arrow row toggles that entry's fold, and
// the unfolded body lines do NOT claim clicks — tool output stays selectable.
func TestMouseToggleFold_EntryArrowTogglesAndBodyFallsThrough(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 40)
	entries := toolClickEntries()
	c.SetEntriesSlice(entries)
	c.groupExpanded[1] = true
	c.rebuild()
	line := findPlainLine(t, &c, "b.go")
	if !c.MouseToggleFold(2, line) {
		t.Fatalf("click on per-entry arrow row %d was not claimed", line)
	}
	if entries[2].Tool.Folded {
		t.Fatal("per-entry arrow click did not unfold the entry")
	}
	c.rebuild()
	bodyLine := findPlainLine(t, &c, `"path":"b.go"`)
	if c.MouseToggleFold(2, bodyLine) {
		t.Fatalf("click on unfolded body line %d was claimed as a fold toggle", bodyLine)
	}
}
