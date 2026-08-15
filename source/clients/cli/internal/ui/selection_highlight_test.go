package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/theme"
)

// The selection must tint the background while keeping the original per-character
// foreground colors — not flatten the whole range to one color.
func TestHighlightRange_PreservesForeground(t *testing.T) {
	line := lipgloss.NewStyle().Foreground(lipgloss.Color("#EA8212")).Render("amber") +
		" plain " +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#00C8E8")).Render("cyan")

	out := highlightRange(line, 0, ansi.StringWidth(line), theme.SelectionBackgroundSGR(theme.Cracker()))

	if !strings.Contains(out, "38;2;234;130;18") {
		t.Errorf("amber foreground was lost: %q", out)
	}
	if !strings.Contains(out, "38;2;0;200;232") {
		t.Errorf("cyan foreground was lost: %q", out)
	}
	if !strings.Contains(out, "48;2;88;130;158") {
		t.Errorf("selection background not applied: %q", out)
	}
}

func TestHighlightRange_BackgroundOnlyKeepsText(t *testing.T) {
	line := "abcdefghij"
	hl := highlightRange(line, 2, 5, theme.SelectionBackgroundSGR(theme.Cracker()))
	// Background-only highlight: stripping ANSI leaves the text byte-for-byte.
	if got := ansi.Strip(hl); got != line {
		t.Errorf("highlight altered the text: %q", got)
	}
	if !strings.Contains(hl, "48;2;88;130;158") {
		t.Errorf("selection background not applied: %q", hl)
	}
}

// A sent-prompt line carries its own background fill (BufferUserLine's navy).
// The selection has to show over it: the selection bg must be stamped back in
// after the line's own background SGR, not overridden by it. Regression for the
// bug where selecting a sent prompt showed no highlight (the fill won).
func TestHighlightRange_WinsOverLineBackground(t *testing.T) {
	selBg := "\x1b[48;2;88;130;158m" // #58829E selection
	lineBg := "\x1b[48;2;17;51;26m"  // #11331A sent-prompt fill (Cracker BufferUserBg)
	line := lipgloss.NewStyle().Background(lipgloss.Color("#11331A")).Render("sent prompt")
	hl := highlightRange(line, 0, ansi.StringWidth(line), selBg)

	// Text is untouched by the overlay.
	if got := ansi.Strip(hl); got != "sent prompt" {
		t.Errorf("highlight altered the text: %q", got)
	}
	if !strings.Contains(hl, selBg) {
		t.Fatalf("selection background not applied: %q", hl)
	}
	// Every occurrence of the line's own fill must be immediately followed by
	// the selection bg, so the selection is the active background over the text
	// rather than being clobbered by the fill (the pre-fix behavior).
	if strings.Contains(hl, lineBg) && !strings.Contains(hl, lineBg+selBg) {
		t.Errorf("line background overrides selection (bug): %q", hl)
	}
}
