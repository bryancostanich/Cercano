// Package ui — overlay.go: z-order compositing for floating UI (modals).
//
// The rest of the TUI renders a single monolithic string per frame — header,
// content page, prompt, status — joined newline-by-newline. Overlays (like
// the local-runtime install modal) need to appear ON TOP of that base frame
// without disturbing the underlying layout. composeOverlay does the splice:
// given a base frame and an opaque box, it replaces the cells under the box
// with the box's cells, leaving everything else untouched. Both inputs may
// contain ANSI escape codes; we lean on charmbracelet/x/ansi for the
// width-aware slicing so styles are preserved on the left and right
// remainders of each affected line.
//
// The overlay itself is expected to be a pre-rendered bordered rectangle
// (lipgloss.Border applied), so all its rows have the same visible width.
// Rows outside the base string, or negative offsets, are silently ignored so
// callers can compute (x, y) from window dimensions without extra bounds
// checks for narrow terminals.
package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// composeOverlay splices box onto base at cell coordinates (x, y). Both may
// contain ANSI sequences. The returned string has the same line count as base
// — the overlay does not add rows.
//
//   - x is a cell column offset (0 = leftmost).
//   - y is a line offset into base (0 = first line).
//   - Lines of box are painted onto lines base[y], base[y+1], … in order.
//   - Lines of box that would fall outside base are dropped.
//   - Short base rows are padded with plain spaces up to x so the overlay
//     still lands at the requested column; the overlay is never truncated
//     to a per-row width. Callers pick (x, y) so the modal fits the frame.
//   - Cells to the left and right of the overlay retain their original ANSI
//     styling. The overlay itself is emitted verbatim between explicit resets
//     so its style bleed doesn't leak into the base remainder.
func composeOverlay(base, box string, x, y int) string {
	if box == "" {
		return base
	}
	baseLines := strings.Split(base, "\n")
	boxLines := strings.Split(box, "\n")
	for i, ov := range boxLines {
		row := y + i
		if row < 0 || row >= len(baseLines) {
			continue
		}
		baseLines[row] = spliceLine(baseLines[row], ov, x)
	}
	return strings.Join(baseLines, "\n")
}

// spliceLine replaces cells [x, x+width(fg)) of bg with fg, preserving
// ANSI styling on the surrounding remainders. If x is past the visible end
// of bg, the gap is padded with plain spaces. The result is clipped so its
// visible width never exceeds the original bg width — the overlay does not
// widen the frame.
func spliceLine(bg, fg string, x int) string {
	if x < 0 {
		// Skip cells that would render before column 0.
		trim := -x
		if trim >= ansi.StringWidth(fg) {
			return bg
		}
		fg = ansi.Cut(fg, trim, ansi.StringWidth(fg))
		x = 0
	}
	bgW := ansi.StringWidth(bg)
	fgW := ansi.StringWidth(fg)
	if fgW == 0 {
		return bg
	}
	// If x is past bg's end, pad the gap with plain spaces so the overlay
	// still lands at the requested column. Common on short lines (e.g. blank
	// spare rows the View pads with).
	var left string
	if x <= bgW {
		left = ansi.Cut(bg, 0, x)
	} else {
		left = bg + strings.Repeat(" ", x-bgW)
	}
	// Right side: whatever bg had past the overlay's trailing edge.
	// If bg ends before that edge, right is empty (overlay extends past bg).
	// The compositor never truncates the overlay to bg's width — the caller
	// picks (x, y) so the modal fits the terminal.
	var right string
	if end := x + fgW; end < bgW {
		right = ansi.Cut(bg, end, bgW)
	}
	// Bracket the overlay with SGR resets so styles don't leak between
	// layers. ansi.Cut restores any active style at the cut boundary in the
	// bg slices, so we only need to isolate the overlay itself.
	return left + resetSGR + fg + resetSGR + right
}

const resetSGR = "\x1b[0m"
