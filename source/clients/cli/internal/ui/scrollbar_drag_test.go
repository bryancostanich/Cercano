package ui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

// buildDragModel makes a Model with an overflowing chat viewport positioned
// like the real layout (top row 2, width-1 content, bar at column width-1).
func buildDragModel() Model {
	const w, vh = 80, 10
	vp := viewport.New(viewport.WithWidth(w-1), viewport.WithHeight(vh))
	vp.SetContent(strings.Repeat("xxxxxxxx\n", 50)) // total 50 > height 10 → overflow
	return Model{
		width:              w,
		height:             vh + 6,
		scrollbarTop:       2, // header(1) + divider(1), no splash
		viewport:           vp,
		viewportPlainLines: plainLines(strings.Repeat("xxxxxxxx\n", 50)),
	}
}

func send(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(Model)
}

// barDrag presses the top of the scrollbar and drags toward the bottom; returns
// the resulting YOffset. A working drag scrolls down; a hijacked one stays 0.
func barDrag(t *testing.T, m Model) int {
	t.Helper()
	m = send(t, m, tea.MouseClickMsg{X: 79, Y: 2, Button: tea.MouseLeft})
	m = send(t, m, tea.MouseMotionMsg{X: 79, Y: 9, Button: tea.MouseLeft})
	return m.viewport.YOffset()
}

// Regression (root cause from live trace): a click in the rightmost column is
// reported by some terminals as X=width — one past the bar at width-1. The grab
// must still register and the drag must scroll.
func TestScrollbarGrabAtRightEdge(t *testing.T) {
	m := buildDragModel() // width 80 → bar at column 79
	m = send(t, m, tea.MouseClickMsg{X: 80, Y: 2, Button: tea.MouseLeft})  // X == width (off by one)
	m = send(t, m, tea.MouseMotionMsg{X: 80, Y: 9, Button: tea.MouseLeft}) // drag down
	if got := m.viewport.YOffset(); got == 0 {
		t.Fatalf("bar grab at right edge (X=width=%d) did not scroll (yoff=0)", 80)
	}
}

// Control: a fresh scrollbar drag with no prior interaction must scroll.
func TestScrollbarDragFresh(t *testing.T) {
	if got := barDrag(t, buildDragModel()); got == 0 {
		t.Fatalf("fresh scrollbar drag did not scroll (yoff=0)")
	}
}

// Regression for the reported bug: after a viewport interaction leaves
// selection.Dragging set (release not seen / not clearing it), grabbing the
// scrollbar and dragging MUST still scroll — the bar press is authoritative and
// an active scrollbar drag must take priority over text selection.
func TestScrollbarDragWinsOverStuckSelection(t *testing.T) {
	m := buildDragModel()
	// Press in viewport text → beginSelection sets selection.Dragging = true,
	// then (modeling the real failing flow) no clearing release arrives.
	m = send(t, m, tea.MouseClickMsg{X: 5, Y: 5, Button: tea.MouseLeft})
	if !m.selection.Dragging {
		t.Fatalf("precondition: expected selection.Dragging true after text press")
	}
	if got := barDrag(t, m); got == 0 {
		t.Fatalf("scrollbar drag hijacked by stuck selection.Dragging (yoff stayed 0)")
	}
}

// Regression: make a real text selection in the viewport (press, motion,
// release → non-empty → auto-copy-on-release), then drag the scrollbar. It must
// scroll.
func TestScrollbarDragAfterViewportSelectAndCopy(t *testing.T) {
	m := buildDragModel()
	m = send(t, m, tea.MouseClickMsg{X: 2, Y: 4, Button: tea.MouseLeft})   // press → beginSelection
	m = send(t, m, tea.MouseMotionMsg{X: 6, Y: 4, Button: tea.MouseLeft})  // drag-select
	m = send(t, m, tea.MouseReleaseMsg{X: 6, Y: 4, Button: tea.MouseLeft}) // release → auto-copy
	t.Logf("after select+copy: selDrag=%v selActive=%v sbDrag=%v",
		m.selection.Dragging, m.selection.Active, m.scrollbarDragging)
	if got := barDrag(t, m); got == 0 {
		t.Fatalf("scrollbar drag after viewport select+copy did not scroll (yoff=0)")
	}
}

// A clean viewport click (press+release) must also leave the bar draggable.
func TestScrollbarDragAfterCleanViewportClick(t *testing.T) {
	m := buildDragModel()
	m = send(t, m, tea.MouseClickMsg{X: 5, Y: 5, Button: tea.MouseLeft})
	m = send(t, m, tea.MouseReleaseMsg{X: 5, Y: 5, Button: tea.MouseLeft})
	if got := barDrag(t, m); got == 0 {
		t.Fatalf("scrollbar drag after clean viewport click did not scroll (yoff=0)")
	}
}
