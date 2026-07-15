package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestPasteColorProbe(t *testing.T) {
	p := newPromptInput()
	p.SetStyles(promptInputStyles{
		Text:        lipgloss.NewStyle().Foreground(lipgloss.Color("#ea8212")),
		Placeholder: lipgloss.NewStyle(),
		Selection:   lipgloss.NewStyle().Reverse(true),
		Chip:        lipgloss.NewStyle(),
	})
	p.SetWidth(40)
	p.MaxHeight = 20
	// Simulate typed text then a paste mid-line that wraps.
	p.InsertString("recap of what happened. i tried to paste ")
	p.InsertString("* single ' apostrophe screwing up markdown formatting [screenshot]")
	p.recalculate()

	view := p.View()
	for i, line := range strings.Split(view, "\n") {
		t.Logf("row %d: %q", i, line)
	}
}
