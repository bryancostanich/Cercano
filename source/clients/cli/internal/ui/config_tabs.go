package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
)

// configTab identifies one tab in the unified configuration surface opened by
// /config, /m, /c, and /theme. The iota order is the left-to-right render
// order of the tab strip.
type configTab int

const (
	configTabGeneral configTab = iota // routing, permissions, server, dev tools (settings form)
	configTabCloud                    // cloud-profiles editor (settings form, cloud section)
	configTabModels                   // the runtime dashboard (local model management)
	configTabUI                       // theme + accent color (settings form)
	configTabContext                  // read-only context viewer for the active conversation
)

// configTabLabels are the visible tab titles, indexed by configTab.
var configTabLabels = []string{"General", "Cloud", "Models", "UI", "Context"}

// configTabCount is the number of tabs; kept as a named constant so wrap-around
// navigation and digit-jump bounds stay in one place.
const configTabCount = 5

func (t configTab) label() string {
	if int(t) < 0 || int(t) >= len(configTabLabels) {
		return ""
	}
	return configTabLabels[t]
}

// clampConfigTab bounds t into the valid range without wrapping.
func clampConfigTab(t configTab) configTab {
	if t < 0 {
		return 0
	}
	if int(t) >= configTabCount {
		return configTabCount - 1
	}
	return t
}

// cycleConfigTab advances the active tab by dir (+1 / -1) with wrap-around,
// so ←/→ at either end rolls to the far side of the strip.
func cycleConfigTab(active configTab, dir int) configTab {
	n := configTabCount
	return configTab((int(active)+dir%n+n) % n)
}

// tabSegment records where one tab sits on the strip row, as a half-open
// column range [start, end). Both the renderer and the click hit-tester read
// these from configTabSegments so their geometry can never disagree.
type tabSegment struct {
	tab        configTab
	start, end int
}

// configTabCellPad is the number of spaces padding each side of a tab label
// inside its cell (so "General" occupies " General ").
const configTabCellPad = 1

// configTabGap is the number of blank columns rendered between adjacent tab
// cells.
const configTabGap = 1

// configTabSegments returns the column geometry of every tab cell, laid out
// left to right. This is the single source of truth for the strip layout.
func configTabSegments() []tabSegment {
	segs := make([]tabSegment, len(configTabLabels))
	x := 0
	for i, lbl := range configTabLabels {
		w := lipgloss.Width(lbl) + 2*configTabCellPad
		segs[i] = tabSegment{tab: configTab(i), start: x, end: x + w}
		x += w + configTabGap
	}
	return segs
}

// renderConfigTabStrip draws the tab bar row. The active tab is filled in the
// accent color; inactive tabs render muted. When focused is true the tab bar
// currently owns keyboard focus (←/→ switch tabs), so the active tab gets a
// brighter, bolder treatment to signal that arrows act on it — mirroring how a
// focused form field brightens.
func renderConfigTabStrip(width int, active configTab, focused bool, s theme.Styles) string {
	active = clampConfigTab(active)
	gap := strings.Repeat(" ", configTabGap)
	var b strings.Builder
	for i, lbl := range configTabLabels {
		if i > 0 {
			b.WriteString(gap)
		}
		cell := strings.Repeat(" ", configTabCellPad) + lbl + strings.Repeat(" ", configTabCellPad)
		switch {
		case configTab(i) == active && focused:
			// Active + focused: reverse-video accent so the arrows' target is
			// unmistakable.
			b.WriteString(s.Accent.Reverse(true).Render(cell))
		case configTab(i) == active:
			// Active but focus is in the body: keep it lit, no reverse.
			b.WriteString(s.Accent.Bold(true).Render(cell))
		default:
			b.WriteString(s.Muted.Render(cell))
		}
	}
	line := b.String()
	// Pad/truncate to the full width so the strip owns its whole row and no
	// stale glyphs bleed through on resize.
	if lipgloss.Width(line) < width {
		line += strings.Repeat(" ", width-lipgloss.Width(line))
	}
	return line
}

// configTabAtX maps a 0-based column on the tab-strip row to the tab whose cell
// contains it, or -1 when the click lands in a gap or past the last tab. The
// gap columns between cells are intentionally dead so a click has to land on a
// label to switch.
func configTabAtX(x int) configTab {
	for _, seg := range configTabSegments() {
		if x >= seg.start && x < seg.end {
			return seg.tab
		}
	}
	return -1
}
