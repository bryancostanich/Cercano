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

// A clean viewport click (press+release) must also leave the bar draggable.
func TestScrollbarDragAfterCleanViewportClick(t *testing.T) {
	m := buildDragModel()
	m = send(t, m, tea.MouseClickMsg{X: 5, Y: 5, Button: tea.MouseLeft})
	m = send(t, m, tea.MouseReleaseMsg{X: 5, Y: 5, Button: tea.MouseLeft})
	if got := barDrag(t, m); got == 0 {
		t.Fatalf("scrollbar drag after clean viewport click did not scroll (yoff=0)")
	}
}
