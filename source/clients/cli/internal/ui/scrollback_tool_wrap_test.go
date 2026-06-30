package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
)

// A folded tool entry whose args are too long even after middle-elision must
// fall through to the wrap path and break across multiple lines, none
// exceeding the width. Width 18 is tight enough that elision refuses
// (post-elide budget < 3), so the wrap path is genuinely exercised.
func TestRenderToolEntryFoldedWrapsToWidth(t *testing.T) {
	e := ToolEntry{
		ToolName:      "Bash",
		ArgsSummary:   strings.Repeat("x", 200), // long, no "/" so segment-elide doesn't apply
		ResultSummary: "ok",
		Status:        ToolStatusComplete,
		Folded:        true,
	}
	// width=20 lands in the wrap window: budget for middle-elision is 2
	// (refused, returns s unchanged), but wrap-avail = width-hang = 9 ≥ 8
	// so the wrap fallback actually fires.
	const width = 20
	got := renderToolEntry(e, width, false, theme.NewStyles(theme.Cracker()), render.NewMarkdown(theme.MarkdownStyle(theme.Cracker())))
	if !strings.Contains(got, "\n") {
		t.Fatalf("long folded entry should wrap to multiple lines, got one line: %q", got)
	}
	for _, ln := range strings.Split(got, "\n") {
		if w := lipgloss.Width(ln); w > width {
			t.Fatalf("wrapped line exceeds width %d (%d cols): %q", width, w, ln)
		}
	}
}

// Wrapped continuation lines hang-indent under the content, aligned past the
// "  ▸ Bash " prefix (visible width 9), so the wrap reads as one entry.
func TestRenderToolEntryFoldedHangingIndent(t *testing.T) {
	e := ToolEntry{ToolName: "Bash", ArgsSummary: strings.Repeat("x", 200), ResultSummary: "ok", Status: ToolStatusComplete, Folded: true}
	lines := strings.Split(renderToolEntry(e, 20, false, theme.NewStyles(theme.Cracker()), render.NewMarkdown(theme.MarkdownStyle(theme.Cracker()))), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected the long entry to wrap")
	}
	const hang = 11 // width of "  ▸ Bash   " (2 gutter + 1 marker + 1 sp + 6 padded name + 1 sp)
	for i, ln := range lines[1:] {
		if !strings.HasPrefix(ln, strings.Repeat(" ", hang)) {
			t.Fatalf("continuation line %d is not hang-indented by %d: %q", i+1, hang, ln)
		}
	}
}

// A folded entry that fits must stay a single line (no spurious wrapping/pad).
func TestRenderToolEntryFoldedShortStaysOneLine(t *testing.T) {
	e := ToolEntry{ToolName: "Read", ArgsSummary: "main.go", ResultSummary: "ok", Status: ToolStatusComplete, Folded: true}
	got := renderToolEntry(e, 80, false, theme.NewStyles(theme.Cracker()), render.NewMarkdown(theme.MarkdownStyle(theme.Cracker())))
	if strings.Contains(got, "\n") {
		t.Fatalf("short folded entry should stay one line, got:\n%q", got)
	}
}
