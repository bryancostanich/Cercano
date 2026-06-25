package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
)

func TestRenderScrollable_WindowsAndPaintsBar(t *testing.T) {
	s := theme.NewStyles(theme.Cracker())
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line"
	}
	out := renderScrollable(lines, 5, 30, 0, s)
	rows := strings.Split(out, "\n")
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5 (height window)", len(rows))
	}
	for _, r := range rows {
		if w := lipgloss.Width(r); w != 32 { // panelW(30) + 1 gutter space + 1 bar glyph
			t.Fatalf("row width = %d, want 32 (panelW + space + bar)", w)
		}
	}
	if !strings.ContainsRune(out, '█') {
		t.Fatalf("expected a scrollbar thumb glyph")
	}
}

func TestRenderScrollable_ShorterThanHeightPadsBlank(t *testing.T) {
	s := theme.NewStyles(theme.Cracker())
	out := renderScrollable([]string{"a", "b"}, 4, 10, 0, s)
	if rows := strings.Split(out, "\n"); len(rows) != 4 {
		t.Fatalf("got %d rows, want 4 (padded to height)", len(rows))
	}
}
