package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The selection must tint the background while keeping the original per-character
// foreground colors — not flatten the whole range to one color.
func TestHighlightRange_PreservesForeground(t *testing.T) {
	line := lipgloss.NewStyle().Foreground(lipgloss.Color("#EA8212")).Render("amber") +
		" plain " +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#00C8E8")).Render("cyan")

	out := highlightRange(line, 0, ansi.StringWidth(line))

	if !strings.Contains(out, "38;2;234;130;18") {
		t.Errorf("amber foreground was lost: %q", out)
	}
	if !strings.Contains(out, "38;2;0;200;232") {
		t.Errorf("cyan foreground was lost: %q", out)
	}
	if !strings.Contains(out, "48;2;45;79;97") {
		t.Errorf("selection background not applied: %q", out)
	}
}

func TestHighlightRange_BackgroundOnlyKeepsText(t *testing.T) {
	line := "abcdefghij"
	hl := highlightRange(line, 2, 5)
	// Background-only highlight: stripping ANSI leaves the text byte-for-byte.
	if got := ansi.Strip(hl); got != line {
		t.Errorf("highlight altered the text: %q", got)
	}
	if !strings.Contains(hl, "48;2;45;79;97") {
		t.Errorf("selection background not applied: %q", hl)
	}
}
