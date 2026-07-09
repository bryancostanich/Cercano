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

// setupChatView loads entries into both the renderer (SetEntries) and the
// internal slice (SetEntriesSlice) the nav helpers read from. In the live
// host these stay in sync via AppendEntry + rebuild; tests do it explicitly.
func setupChatView(c *chatView, entries []*Entry) {
	c.SetEntriesSlice(entries)
	c.SetEntries(entries)
}

// renderChatView re-renders the current c.entries and returns the ANSI-stripped
// scrollback text. Used after each state change so tests see the new view.
func renderChatView(c *chatView) string {
	c.SetEntries(c.entries)
	return stripAnsiCSI(strings.Join(c.PlainLines(), "\n"))
}

// In a collapsed multi-entry group, the focus marker (▶) appears on the
// summary line whenever any entry in the group is the focused entry —
// keyboard nav focuses the group as a whole until the user expands it.
func TestChatView_CollapsedGroupFocusMarkerOnSummary(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 20)
	setupChatView(&c, []*Entry{
		{Tool: &ToolEntry{ToolName: "Read", ArgsSummary: "a.go", Status: ToolStatusComplete, Duration: 5 * time.Millisecond, Folded: true}},
		{Tool: &ToolEntry{ToolName: "Edit", ArgsSummary: "b.go", Status: ToolStatusComplete, Duration: 7 * time.Millisecond, Folded: true}},
	})
	if got := stripAnsiCSI(strings.Join(c.PlainLines(), "\n")); strings.Contains(got, "▶") {
		t.Fatalf("no focus marker expected before EnterToolNav, got:\n%s", got)
	}
	if !c.EnterToolNav() {
		t.Fatal("EnterToolNav returned false on a transcript with tool entries")
	}
	got := renderChatView(&c)
	if !strings.Contains(got, "▶") {
		t.Errorf("focus marker expected on collapsed group summary after EnterToolNav, got:\n%s", got)
	}
	if !strings.Contains(got, "2 tool calls") {
		t.Errorf("group should remain collapsed (still showing summary) after focus, got:\n%s", got)
	}
}

// ToggleFocusedFold on a collapsed multi-entry group EXPANDS the group rather
// than toggling the entry's Folded — the first Enter is "open the group".
func TestChatView_ToggleExpandsCollapsedMultiEntryGroup(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 20)
	setupChatView(&c, []*Entry{
		{Tool: &ToolEntry{ToolName: "Read", ArgsSummary: "a.go", Status: ToolStatusComplete, Duration: 5 * time.Millisecond, Folded: true}},
		{Tool: &ToolEntry{ToolName: "Edit", ArgsSummary: "b.go", Status: ToolStatusComplete, Duration: 7 * time.Millisecond, Folded: true}},
	})
	c.EnterToolNav()
	c.ToggleFocusedFold()
	got := renderChatView(&c)
	// Expanding keeps the summary as a ▾ collapse header and reveals each
	// per-call line beneath it.
	if !strings.Contains(got, "▾") || !strings.Contains(got, "2 tool calls") {
		t.Errorf("after expand, a ▾ collapse header with the count should remain, got:\n%s", got)
	}
	if !strings.Contains(got, "a.go") || !strings.Contains(got, "b.go") {
		t.Errorf("expanded group should show both per-call entries, got:\n%s", got)
	}
}

// Clicking the group's summary header round-trips expand ⇄ collapse — the fix
// for "expands but there's no way to unexpand". The header sits at content line
// 0; the first click expands (▸ summary → per-call lines under a ▾ header), the
// second click on the same header collapses back to the rolling summary.
func TestChatView_HeaderClickRoundTripsGroupExpandCollapse(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 20)
	setupChatView(&c, []*Entry{
		{Tool: &ToolEntry{ToolName: "Read", ArgsSummary: "a.go", Status: ToolStatusComplete, Duration: 5 * time.Millisecond, Folded: true}},
		{Tool: &ToolEntry{ToolName: "Edit", ArgsSummary: "b.go", Status: ToolStatusComplete, Duration: 7 * time.Millisecond, Folded: true}},
	})
	// Collapsed: rolling summary present, per-call args folded away.
	got := renderChatView(&c)
	if !strings.Contains(got, "2 tool calls") || strings.Contains(got, "a.go") {
		t.Fatalf("precondition: expected collapsed summary without per-call args, got:\n%s", got)
	}
	// Click the summary header (content line 0) → expands to per-call lines.
	if !c.MouseToggleFold(4, 0) {
		t.Fatal("click on collapsed group summary header should be handled")
	}
	got = renderChatView(&c)
	if !strings.Contains(got, "a.go") || !strings.Contains(got, "b.go") {
		t.Fatalf("after header click the group should expand to per-call lines, got:\n%s", got)
	}
	// Click the header again (still content line 0, now ▾) → collapses back.
	if !c.MouseToggleFold(4, 0) {
		t.Fatal("click on expanded group header should be handled")
	}
	got = renderChatView(&c)
	if !strings.Contains(got, "2 tool calls") || strings.Contains(got, "a.go") {
		t.Errorf("after second header click the group should collapse back to the summary, got:\n%s", got)
	}
}

// After expansion, ToggleFocusedFold toggles the focused entry's Folded —
// the second Enter is "open the entry's body" (full args + result).
func TestChatView_ToggleAfterExpandUnfoldsFocusedEntry(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 20)
	setupChatView(&c, []*Entry{
		{Tool: &ToolEntry{ToolName: "Read", ArgsSummary: "a.go", FullArgs: `{"path":"a.go"}`, Status: ToolStatusComplete, Duration: 5 * time.Millisecond, Folded: true}},
		{Tool: &ToolEntry{ToolName: "Edit", ArgsSummary: "b.go", FullArgs: `{"path":"b.go","old_string":"old line","new_string":"new line"}`, Status: ToolStatusComplete, Duration: 7 * time.Millisecond, Folded: true}},
	})
	c.EnterToolNav()      // focuses last (Edit)
	c.ToggleFocusedFold() // expand group
	c.ToggleFocusedFold() // toggle focused entry's Folded
	got := renderChatView(&c)
	// The Edit entry's body is a diff of its args (old_string → new_string);
	// the new_string only appears once the entry is unfolded.
	if !strings.Contains(got, "new line") {
		t.Errorf("expected focused (Edit) entry's diff body after second toggle, got:\n%s", got)
	}
	if strings.Contains(got, `"path":"a.go"`) {
		t.Errorf("unfocused (Read) entry should stay folded, got:\n%s", got)
	}
}

// A single-entry "group" has no group-expansion step — ToggleFocusedFold
// directly toggles the entry's Folded on the first press.
func TestChatView_SingleEntryGroupTogglesEntryFoldImmediately(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 100, 20)
	setupChatView(&c, []*Entry{
		{Tool: &ToolEntry{ToolName: "Read", ArgsSummary: "a.go", FullArgs: `{"path":"a.go"}`, Status: ToolStatusComplete, Duration: 5 * time.Millisecond, Folded: true}},
	})
	c.EnterToolNav()
	c.ToggleFocusedFold()
	got := renderChatView(&c)
	if !strings.Contains(got, `"path":"a.go"`) {
		t.Errorf("single-entry group should show FullArgs after one toggle, got:\n%s", got)
	}
}

// In a mixed group (some completed, one in-progress), the summary line counts
// the whole run — completed calls plus the in-flight one — and the active
// entry still renders live below it. Counting the whole run keeps the header
// from under-reporting (and jumping) as the live call lands.
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

	if !strings.Contains(s, "3 tool calls") {
		t.Errorf("expected '3 tool calls' summary (2 completed + 1 in-flight), got:\n%s", s)
	}
	// Active entry uses the verb form from Phase A.
	if !strings.Contains(s, "Editing") {
		t.Errorf("expected active entry rendered with verb 'Editing', got:\n%s", s)
	}
}
