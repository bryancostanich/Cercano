package ui

import (
	"strings"
	"testing"
	"time"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
)

// defaultOpts returns the opts used by tests that don't care about expansion or
// focus: collapsed, no entry focused.
func defaultOpts() groupRenderOpts { return groupRenderOpts{FocusedIdx: -1} }

func makeCompletedTool(name string, dur time.Duration) ToolEntry {
	return ToolEntry{
		ToolName:    name,
		ArgsSummary: "foo.go",
		Status:      ToolStatusComplete,
		Duration:    dur,
		Folded:      true,
	}
}

// Single-entry runs render as a per-call line, NOT a "1 tool call" summary —
// folding only compresses when there's a meaningful "many". The summary
// would hide the args+result blurb without any savings.
func TestRenderToolGroup_SingleEntryPassesThroughToPerCallLine(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	md := render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))
	got := renderToolGroup(
		[]ToolEntry{makeCompletedTool("Read", 8*time.Millisecond)},
		80, styles, md, defaultOpts(),
	)
	s := stripAnsiCSI(got)
	if strings.Contains(s, "tool call") {
		t.Errorf("single entry must not render a 'tool call' summary, got: %q", s)
	}
	if !strings.Contains(s, "Read") {
		t.Errorf("expected per-call line to mention the tool name, got: %q", s)
	}
	if !strings.Contains(s, "foo.go") {
		t.Errorf("expected per-call line to show args, got: %q", s)
	}
	if strings.Contains(s, "\n") {
		t.Errorf("single per-call line should not wrap, got: %q", s)
	}
}

func TestRenderToolGroup_DaylightSummaryLabelUsesReadablePrimary(t *testing.T) {
	var daylight theme.Palette
	for _, builtin := range theme.BuiltinThemes() {
		if builtin.Name == "daylight" {
			daylight = builtin.Palette
			break
		}
	}
	styles := theme.NewStyles(daylight)
	md := render.NewMarkdown(theme.MarkdownStyle(daylight))
	entries := []ToolEntry{
		makeCompletedTool("Read", 5*time.Millisecond),
		makeCompletedTool("Edit", 7*time.Millisecond),
	}
	out := renderToolGroup(entries, 100, styles, md, defaultOpts())
	if !strings.Contains(out, "38;2;90;58;10") {
		t.Fatalf("daylight tool group summary marker/count should use primary text (#5A3A0A), got %q", out)
	}
	if strings.Contains(out, "\x1b[2m▸") || strings.Contains(out, "\x1b[2m2 tool calls") {
		t.Fatalf("tool group summary marker/count should not be faint in daylight, got %q", out)
	}
}

func TestRenderToolGroup_PluralLabelWithBreakdown(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	md := render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))
	entries := []ToolEntry{
		makeCompletedTool("Read", 5*time.Millisecond),
		makeCompletedTool("Read", 7*time.Millisecond),
		makeCompletedTool("Read", 3*time.Millisecond),
		makeCompletedTool("Edit", 12*time.Millisecond),
		makeCompletedTool("Edit", 8*time.Millisecond),
		makeCompletedTool("Bash", 50*time.Millisecond),
		makeCompletedTool("Write", 4*time.Millisecond),
	}
	s := stripAnsiCSI(renderToolGroup(entries, 100, styles, md, defaultOpts()))
	if !strings.Contains(s, "7 tool calls") {
		t.Errorf("expected '7 tool calls', got: %q", s)
	}
	if !strings.Contains(s, "(3 Read, 2 Edit, Bash, Write)") {
		t.Errorf("breakdown wrong: %q", s)
	}
	if !strings.Contains(s, "89ms") {
		t.Errorf("total duration missing or wrong: %q", s)
	}
	if strings.Contains(s, "\n") {
		t.Errorf("all-completed group should render as one line: %q", s)
	}
}

func TestRenderToolGroup_ErrorGlyphWhenAnyFails(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	md := render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))
	entries := []ToolEntry{
		makeCompletedTool("Read", 5*time.Millisecond),
		{ToolName: "Bash", ArgsSummary: "go test", Status: ToolStatusError, Duration: 12 * time.Millisecond, Folded: true},
	}
	s := stripAnsiCSI(renderToolGroup(entries, 80, styles, md, defaultOpts()))
	if !strings.Contains(s, "⚠") {
		t.Errorf("expected error glyph ⚠ when any entry errored, got: %q", s)
	}
	if strings.Contains(s, "✓") {
		t.Errorf("success glyph leaked through: %q", s)
	}
}

func TestRenderToolGroup_InProgressOnlyShowsActiveLineOnly(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	md := render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))
	entries := []ToolEntry{
		{ToolName: "read", ArgsSummary: "foo.go", Status: ToolStatusInProgress, Folded: true},
	}
	s := stripAnsiCSI(renderToolGroup(entries, 80, styles, md, defaultOpts()))
	if strings.Contains(s, "tool call") {
		t.Errorf("no summary expected when only in-progress, got: %q", s)
	}
	if !strings.Contains(s, "Reading") {
		t.Errorf("expected verb form 'Reading' in active row: %q", s)
	}
}

func TestRenderToolGroup_MixedShowsSummaryThenActive(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	md := render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))
	entries := []ToolEntry{
		makeCompletedTool("Read", 8*time.Millisecond),
		makeCompletedTool("Edit", 5*time.Millisecond),
		{ToolName: "bash", ArgsSummary: "go build", Status: ToolStatusInProgress, Folded: true},
	}
	s := stripAnsiCSI(renderToolGroup(entries, 80, styles, md, defaultOpts()))
	lines := strings.Split(s, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (summary + active), got %d: %q", len(lines), s)
	}
	if !strings.Contains(lines[0], "3 tool calls") {
		t.Errorf("first line should be summary counting the whole run (2 completed + 1 in-flight): %q", lines[0])
	}
	if !strings.Contains(lines[1], "Running") {
		t.Errorf("second line should be the active entry with verb form: %q", lines[1])
	}
}

func TestRenderGroupSummary_RightAlignsTiming(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	entries := []ToolEntry{makeCompletedTool("Read", 10*time.Millisecond)}
	s := stripAnsiCSI(renderGroupSummary(entries, 80, styles, false, false))
	if !strings.HasSuffix(strings.TrimRight(s, " "), "10ms ✓") {
		t.Errorf("expected right-aligned '10ms ✓' suffix, got: %q", s)
	}
	if !strings.Contains(s, strings.Repeat(" ", 20)) {
		t.Errorf("expected substantial padding between label and timing, got: %q", s)
	}
}

// Expanded multi-entry group renders a ▾ summary header (the click-to-collapse
// affordance) followed by each entry as its own per-call line.
func TestRenderToolGroup_ExpandedRendersPerCallLines(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	md := render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))
	entries := []ToolEntry{
		makeCompletedTool("Read", 5*time.Millisecond),
		makeCompletedTool("Edit", 7*time.Millisecond),
		makeCompletedTool("Bash", 12*time.Millisecond),
	}
	s := stripAnsiCSI(renderToolGroup(entries, 100, styles, md, groupRenderOpts{Expanded: true, FocusedIdx: -1}))
	lines := strings.Split(s, "\n")
	// Header (▾ + count) plus one per-call line per entry.
	if len(lines) != 4 {
		t.Fatalf("expected 1 header + 3 per-call lines, got %d: %q", len(lines), s)
	}
	if !strings.Contains(lines[0], "▾") || !strings.Contains(lines[0], "3 tool calls") {
		t.Errorf("line 0 should be the ▾ collapse header with the count, got: %q", lines[0])
	}
	for i, want := range []string{"Read", "Edit", "Bash"} {
		if !strings.Contains(lines[i+1], want) {
			t.Errorf("line %d should mention %q: %q", i+1, want, lines[i+1])
		}
	}
}

// Collapsed group with Focused=true shows the accent ▶ on the summary line's
// gutter — the same marker renderToolEntry uses for per-entry focus.
func TestRenderToolGroup_CollapsedFocusedShowsAccentMarker(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	md := render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))
	entries := []ToolEntry{
		makeCompletedTool("Read", 5*time.Millisecond),
		makeCompletedTool("Edit", 7*time.Millisecond),
	}
	unfocused := stripAnsiCSI(renderToolGroup(entries, 100, styles, md, groupRenderOpts{FocusedIdx: -1}))
	focused := stripAnsiCSI(renderToolGroup(entries, 100, styles, md, groupRenderOpts{Focused: true, FocusedIdx: -1}))
	if !strings.Contains(focused, "▶") {
		t.Errorf("focused summary should contain ▶ marker, got: %q", focused)
	}
	if strings.Contains(unfocused, "▶") {
		t.Errorf("unfocused summary must not contain ▶ marker, got: %q", unfocused)
	}
}

// Expanded group with FocusedIdx pointing to a specific entry shows the ▶
// marker on that entry's line only.
func TestRenderToolGroup_ExpandedFocusedMarksFocusedEntry(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	md := render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))
	entries := []ToolEntry{
		makeCompletedTool("Read", 5*time.Millisecond),
		makeCompletedTool("Edit", 7*time.Millisecond),
		makeCompletedTool("Bash", 12*time.Millisecond),
	}
	s := stripAnsiCSI(renderToolGroup(entries, 100, styles, md, groupRenderOpts{Expanded: true, FocusedIdx: 1}))
	lines := strings.Split(s, "\n")
	// Line 0 is the ▾ collapse header; per-call lines follow at 1..3.
	if len(lines) != 4 {
		t.Fatalf("expected 1 header + 3 per-call lines, got %d: %q", len(lines), s)
	}
	if strings.Contains(lines[0], "▶") {
		t.Errorf("header line should not carry entry focus: %q", lines[0])
	}
	if strings.Contains(lines[1], "▶") {
		t.Errorf("line 1 (Read) should not be focused: %q", lines[1])
	}
	if !strings.Contains(lines[2], "▶") {
		t.Errorf("line 2 (Edit) should be focused with ▶ marker: %q", lines[2])
	}
	if strings.Contains(lines[3], "▶") {
		t.Errorf("line 3 (Bash) should not be focused: %q", lines[3])
	}
}
