package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
)

// tabStripItem is one visible cell in a reusable single-row tab strip.
type tabStripItem struct {
	ID    string
	Label string
}

type tabStripSegment struct {
	id         string
	start, end int
}

const tabStripCellPad = 1
const tabStripGap = 1

func tabStripSegments(items []tabStripItem) []tabStripSegment {
	segs := make([]tabStripSegment, 0, len(items))
	x := 0
	for _, item := range items {
		w := lipgloss.Width(item.Label) + 2*tabStripCellPad
		segs = append(segs, tabStripSegment{id: item.ID, start: x, end: x + w})
		x += w + tabStripGap
	}
	return segs
}

func renderTabStrip(width int, items []tabStripItem, active string, focused bool, s theme.Styles) string {
	gap := strings.Repeat(" ", tabStripGap)
	var b strings.Builder
	for i, item := range items {
		if i > 0 {
			b.WriteString(gap)
		}
		cell := strings.Repeat(" ", tabStripCellPad) + item.Label + strings.Repeat(" ", tabStripCellPad)
		switch {
		case item.ID == active && focused:
			b.WriteString(s.Accent.Reverse(true).Render(cell))
		case item.ID == active:
			b.WriteString(s.Accent.Bold(true).Render(cell))
		default:
			b.WriteString(s.Muted.Render(cell))
		}
	}
	line := b.String()
	if lipgloss.Width(line) < width {
		line += strings.Repeat(" ", width-lipgloss.Width(line))
	}
	return line
}

func tabStripAtX(items []tabStripItem, x int) (string, bool) {
	for _, seg := range tabStripSegments(items) {
		if x >= seg.start && x < seg.end {
			return seg.id, true
		}
	}
	return "", false
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
