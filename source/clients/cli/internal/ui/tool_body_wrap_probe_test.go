package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
)

// PROBE: reproduce the "wrapped code spills left of the rail" bug.
// A highlighted (Glamour-fenced) result with lines longer than the width
// budget must still come back with every physical line <= width, because
// anything wider soft-wraps downstream at full terminal width with no indent
// and no rail.
func TestProbe_ExpandedHighlightedBodyLinesFitWidth(t *testing.T) {
	palette := theme.Cracker()
	styles := theme.NewStyles(palette)
	md := render.NewMarkdown(theme.MarkdownStyle(palette))

	longLine := `profiles := []ProfileRef{{Name: "chat backendless deepinfra", BaseURL: "https://api.deepinfra.com/v1/openai", Key: "deepinfra"}, {Name: "chat backendless together", BaseURL: "https://api.together.xyz/v1", Key: "together"}}`
	result := "package main\n\nfunc seed() {\n\t" + longLine + "\n}\n"

	const width = 100
	e := ToolEntry{
		ToolName:   "Read",
		Status:     ToolStatusComplete,
		Folded:     false,
		FullArgs:   `{"path":"source/server/internal/cloudcatalog/catalog.go"}`,
		FullResult: result,
	}
	out := renderToolEntry(e, width, false, styles, md)
	for i, l := range strings.Split(out, "\n") {
		if w := lipgloss.Width(l); w > width {
			t.Errorf("line %d exceeds width budget: %d > %d: %q", i, w, width, l)
		}
	}
}

// Same probe for the diff path: an Edit whose new_string has an over-long line.
func TestProbe_ExpandedDiffBodyLinesFitWidth(t *testing.T) {
	palette := theme.Cracker()
	styles := theme.NewStyles(palette)
	md := render.NewMarkdown(theme.MarkdownStyle(palette))

	long := strings.Repeat("x", 180)
	e := ToolEntry{
		ToolName: "Edit",
		Status:   ToolStatusComplete,
		Folded:   false,
		FullArgs: `{"path":"a.go","old_string":"old line","new_string":"` + long + `"}`,
	}
	const width = 100
	out := renderToolEntry(e, width, false, styles, md)
	for i, l := range strings.Split(out, "\n") {
		if w := lipgloss.Width(l); w > width {
			t.Errorf("diff line %d exceeds width budget: %d > %d: %q", i, w, width, l)
		}
	}
}
