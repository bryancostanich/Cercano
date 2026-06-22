package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Regression: Glamour pads prose a few columns past the wrap width. Without
// clamping, the composited scrollbar row exceeds the terminal width, wraps, and
// the scrollbar vanishes when prose scrolls into view. Every composited row must
// stay exactly m.width, and the scrollbar must remain present, at every scroll
// position.
func TestScrollbar_NoWidthOverflowWithProse(t *testing.T) {
	const termW = 100
	var sb strings.Builder
	sb.WriteString("# A Heading That Is Reasonably Long For Wrapping\n\n")
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&sb, "## Section %d\n\nbody paragraph %d with enough words to form a full line of prose.\n\n", i, i)
	}

	m := New(nil, false)
	m = m.SeedAssistantMarkdown(sb.String())
	m = send(t, m, tea.WindowSizeMsg{Width: termW, Height: 30})

	assertOK := func(label string) {
		t.Helper()
		rows := strings.Split(m.renderViewportWithScrollbar(), "\n")
		bar := false
		for i, r := range rows {
			if w := ansi.StringWidth(r); w > termW {
				t.Fatalf("%s: row %d width %d exceeds terminal width %d (would wrap and hide the scrollbar): %q",
					label, i, w, termW, ansi.Strip(r))
			}
			if strings.Contains(r, "█") || strings.Contains(r, "░") {
				bar = true
			}
		}
		if !bar {
			t.Fatalf("%s: scrollbar absent despite overflowing content", label)
		}
	}

	assertOK("initial")
	for i := 0; i < 100; i++ {
		m = send(t, m, tea.MouseWheelMsg{Button: ansi.MouseWheelUp})
	}
	assertOK("scrolled to top")
}
