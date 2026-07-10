package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
)

// tabStripItem is one visible cell in a reusable single-row tab strip.
type tabStripItem struct {
	ID       string
	Label    string
	Closable bool // render a clickable [x] affix (ephemeral sub-agent tabs)
}

// tabStripCloseGlyph is the clickable close affix rendered inside a closable
// cell as " ✕".
const tabStripCloseGlyph = "✕"

type tabStripSegment struct {
	id         string
	start, end int
	// closeStart is the first column of the [x] hit region for a closable
	// cell (0 when the cell has no close button).
	closeStart int
}

const tabStripCellPad = 1
const tabStripGap = 1

func tabStripSegments(items []tabStripItem) []tabStripSegment {
	segs := make([]tabStripSegment, 0, len(items))
	// The first tab starts at column 1: the leading end bar (see renderTabStrip)
	// occupies column 0. The gap added after each tab covers the bar before the
	// next one.
	x := 1
	for _, item := range items {
		labelW := lipgloss.Width(item.Label)
		w := labelW + 2*tabStripCellPad
		closeStart := 0
		if item.Closable {
			// Cell layout: [pad][label][space][✕][pad]. The close hit region spans
			// the space + glyph + trailing pad so a click near the ✕ counts.
			closeStart = x + tabStripCellPad + labelW
			w += 2 // " ✕"
		}
		segs = append(segs, tabStripSegment{id: item.ID, start: x, end: x + w, closeStart: closeStart})
		x += w + tabStripGap
	}
	return segs
}

func renderTabStrip(width int, items []tabStripItem, active string, focused bool, s theme.Styles) string {
	var b strings.Builder
	// A dim vertical bar brackets every tab: a left end bar before the first,
	// a separator between adjacent tabs, and a right end bar after the last
	// (written after the loop). Kept in sync with tabStripSegments, which starts
	// the first tab at column 1 to account for the leading bar.
	bar := s.Muted.Render("│")
	for _, item := range items {
		b.WriteString(bar)
		label := item.Label
		if item.Closable {
			label += " " + tabStripCloseGlyph
		}
		cell := strings.Repeat(" ", tabStripCellPad) + label + strings.Repeat(" ", tabStripCellPad)
		switch {
		case item.ID == active && focused:
			// Focused active tab: filled accent background + bold, signaling the
			// strip owns the keyboard.
			b.WriteString(s.Accent.Reverse(true).Bold(true).Render(cell))
		case item.ID == active:
			// Active tab always carries a filled background so the selection reads
			// even when the strip isn't focused.
			b.WriteString(s.Accent.Reverse(true).Render(cell))
		default:
			b.WriteString(s.Muted.Render(cell))
		}
	}
	if len(items) > 0 {
		b.WriteString(bar) // right end bar after the last tab
	}
	line := b.String()
	if lipgloss.Width(line) < width {
		line += strings.Repeat(" ", width-lipgloss.Width(line))
	}
	return line
}

// tabStripAtX maps a column to the tab whose cell contains it (gaps are dead).
func tabStripAtX(items []tabStripItem, x int) (string, bool) {
	id, _, ok := tabStripHitAtX(items, x)
	return id, ok
}

// tabStripHitAtX maps a column to a tab and reports whether the column landed
// on that tab's [x] close button.
func tabStripHitAtX(items []tabStripItem, x int) (id string, isClose bool, ok bool) {
	for _, seg := range tabStripSegments(items) {
		if x >= seg.start && x < seg.end {
			return seg.id, seg.closeStart > 0 && x >= seg.closeStart, true
		}
	}
	return "", false, false
}

func cycleTabStrip(items []tabStripItem, active string, dir int) string {
	if len(items) == 0 {
		return ""
	}
	idx := 0
	for i, item := range items {
		if item.ID == active {
			idx = i
			break
		}
	}
	n := len(items)
	return items[(idx+dir%n+n)%n].ID
}

func clampTabStrip(items []tabStripItem, active string) string {
	if len(items) == 0 {
		return ""
	}
	for _, item := range items {
		if item.ID == active {
			return active
		}
	}
	return items[0].ID
}
