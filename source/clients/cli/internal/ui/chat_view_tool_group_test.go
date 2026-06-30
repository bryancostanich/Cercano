package ui

import (
	"strings"
	"testing"
	"time"

	"cercano/source/clients/cli/internal/theme"
)

// SetEntries must walk contiguous Tool-bearing entries as one group block.
// Three completed tools in a row should produce a single summary line, not
// three standalone per-call lines.
func TestChatView_GroupsContiguousToolEntries(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 20)
	entries := []*Entry{
		{Role: RoleUser, Content: "do stuff"},
		{Role: RoleAssistant, Content: "running tools"},
		{Tool: &ToolEntry{ToolName: "Read", ArgsSummary: "a.go", Status: ToolStatusComplete, Duration: 5 * time.Millisecond, Folded: true}},
		{Tool: &ToolEntry{ToolName: "Read", ArgsSummary: "b.go", Status: ToolStatusComplete, Duration: 7 * time.Millisecond, Folded: true}},
		{Tool: &ToolEntry{ToolName: "Edit", ArgsSummary: "c.go", Status: ToolStatusComplete, Duration: 12 * time.Millisecond, Folded: true}},
	}
	c.SetEntries(entries)
	s := stripAnsiCSI(strings.Join(c.PlainLines(), "\n"))

	if !strings.Contains(s, "3 tool calls") {
		t.Errorf("expected '3 tool calls' summary, got:\n%s", s)
	}
	if !strings.Contains(s, "(2 Read, Edit)") {
		t.Errorf("expected breakdown '(2 Read, Edit)', got:\n%s", s)
	}
	if !strings.Contains(s, "24ms") {
		t.Errorf("expected total '24ms', got:\n%s", s)
	}
}

// Prose between tool runs splits the group: each side renders as its own
// block (or single passthrough for a one-tool side).
func TestChatView_ProseSplitsToolGroups(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 20)
	entries := []*Entry{
		{Role: RoleUser, Content: "first"},
		{Tool: &ToolEntry{ToolName: "Read", ArgsSummary: "a.go", Status: ToolStatusComplete, Duration: 5 * time.Millisecond, Folded: true}},
		{Tool: &ToolEntry{ToolName: "Read", ArgsSummary: "b.go", Status: ToolStatusComplete, Duration: 5 * time.Millisecond, Folded: true}},
		{Role: RoleAssistant, Content: "interlude"},
		{Tool: &ToolEntry{ToolName: "Bash", ArgsSummary: "go test", Status: ToolStatusComplete, Duration: 30 * time.Millisecond, Folded: true}},
	}
	c.SetEntries(entries)
	s := stripAnsiCSI(strings.Join(c.PlainLines(), "\n"))

	if !strings.Contains(s, "2 tool calls") {
		t.Errorf("expected '2 tool calls' summary for first group, got:\n%s", s)
	}
	if !strings.Contains(s, "interlude") {
		t.Errorf("expected prose 'interlude' between groups, got:\n%s", s)
	}
	if !strings.Contains(s, "go test") {
		t.Errorf("expected single-entry passthrough for second group ('go test' args), got:\n%s", s)
	}
}

// In a mixed group (some completed, one in-progress), the summary line covers
// the completed entries and the active entry renders standalone below it.
func TestChatView_MixedGroupShowsSummaryThenActive(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 20)
	entries := []*Entry{
		{Tool: &ToolEntry{ToolName: "Read", ArgsSummary: "a.go", Status: ToolStatusComplete, Duration: 5 * time.Millisecond, Folded: true}},
		{Tool: &ToolEntry{ToolName: "Read", ArgsSummary: "b.go", Status: ToolStatusComplete, Duration: 5 * time.Millisecond, Folded: true}},
		{Tool: &ToolEntry{ToolName: "edit", ArgsSummary: "c.go", Status: ToolStatusInProgress, Folded: true}},
	}
	c.SetEntries(entries)
	s := stripAnsiCSI(strings.Join(c.PlainLines(), "\n"))

	if !strings.Contains(s, "2 tool calls") {
		t.Errorf("expected '2 tool calls' summary, got:\n%s", s)
	}
	// Active entry uses the verb form from Phase A.
	if !strings.Contains(s, "Editing") {
		t.Errorf("expected active entry rendered with verb 'Editing', got:\n%s", s)
	}
}
