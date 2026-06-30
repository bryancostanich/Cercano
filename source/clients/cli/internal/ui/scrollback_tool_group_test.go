package ui

import (
	"strings"
	"testing"
	"time"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
)

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
		80, styles, md,
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
	s := stripAnsiCSI(renderToolGroup(entries, 100, styles, md))
	if !strings.Contains(s, "7 tool calls") {
		t.Errorf("expected '7 tool calls', got: %q", s)
	}
	// Breakdown: first-seen order with prefix counts where n > 1.
	if !strings.Contains(s, "(3 Read, 2 Edit, Bash, Write)") {
		t.Errorf("breakdown wrong: %q", s)
	}
	// Total duration is sum of all entries: 89ms — formatDur rounds to ms.
	if !strings.Contains(s, "89ms") {
		t.Errorf("total duration missing or wrong: %q", s)
	}
	// Should be one line (group summary, no in-progress).
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
	s := stripAnsiCSI(renderToolGroup(entries, 80, styles, md))
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
	s := stripAnsiCSI(renderToolGroup(entries, 80, styles, md))
	if strings.Contains(s, "tool call") {
		t.Errorf("no summary expected when only in-progress, got: %q", s)
	}
	// Active entry uses the verb form ("Reading") per Phase A.
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
	s := stripAnsiCSI(renderToolGroup(entries, 80, styles, md))
	lines := strings.Split(s, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (summary + active), got %d: %q", len(lines), s)
	}
	if !strings.Contains(lines[0], "2 tool calls") {
		t.Errorf("first line should be summary: %q", lines[0])
	}
	if !strings.Contains(lines[1], "Running") {
		t.Errorf("second line should be the active entry with verb form: %q", lines[1])
	}
}

func TestRenderGroupSummary_RightAlignsTiming(t *testing.T) {
	styles := theme.NewStyles(theme.Cracker())
	entries := []ToolEntry{makeCompletedTool("Read", 10*time.Millisecond)}
	s := stripAnsiCSI(renderGroupSummary(entries, 80, styles))
	// Right-alignment means: (a) the label is left-anchored, (b) the timing
	// ends the line, (c) padding separates them. Exact pixel width can drift
	// when terminals disagree on ambiguous-width glyphs (✓), so we don't
	// require a specific total length — just that the timing+glyph end the
	// line and at least a handful of padding spaces sit between them and
	// the label.
	if !strings.HasSuffix(strings.TrimRight(s, " "), "10ms ✓") {
		t.Errorf("expected right-aligned '10ms ✓' suffix, got: %q", s)
	}
	// At least 20 padding spaces between label and timing — confirms the
	// renderer is actually right-aligning rather than just putting them adjacent.
	if !strings.Contains(s, strings.Repeat(" ", 20)) {
		t.Errorf("expected substantial padding between label and timing, got: %q", s)
	}
}
