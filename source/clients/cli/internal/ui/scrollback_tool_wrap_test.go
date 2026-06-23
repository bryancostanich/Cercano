package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// A folded tool entry that is wider than the viewport must WRAP to multiple
// lines, none exceeding the width — not get clipped at the terminal edge.
func TestRenderToolEntryFoldedWrapsToWidth(t *testing.T) {
	e := ToolEntry{
		ToolName:      "Bash",
		ArgsSummary:   strings.Repeat("x", 200), // long, forces overflow
		ResultSummary: "exit 0 · 5ms",
		Status:        ToolStatusComplete,
		Folded:        true,
	}
	const width = 40
	got := renderToolEntry(e, width, false)
	if !strings.Contains(got, "\n") {
		t.Fatalf("long folded entry should wrap to multiple lines, got one line")
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
	lines := strings.Split(renderToolEntry(e, 40, false), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected the long entry to wrap")
	}
	const hang = 9 // width of "  ▸ Bash "
	for i, ln := range lines[1:] {
		if !strings.HasPrefix(ln, strings.Repeat(" ", hang)) {
			t.Fatalf("continuation line %d is not hang-indented by %d: %q", i+1, hang, ln)
		}
	}
}

// A folded entry that fits must stay a single line (no spurious wrapping/pad).
func TestRenderToolEntryFoldedShortStaysOneLine(t *testing.T) {
	e := ToolEntry{ToolName: "Read", ArgsSummary: "main.go", ResultSummary: "ok", Status: ToolStatusComplete, Folded: true}
	got := renderToolEntry(e, 80, false)
	if strings.Contains(got, "\n") {
		t.Fatalf("short folded entry should stay one line, got:\n%q", got)
	}
}
