package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
)

// buildDragModel makes a Model with an overflowing chat viewport positioned
// like the real layout (top row 2, width-1 content, bar at column width-1).
func buildDragModel() Model {
	const w, vh = 80, 10
	p := theme.Cracker()
	cv := newChatView(theme.NewStyles(p), p, w-1, vh)
	content := strings.Repeat("xxxxxxxx\n", 50) // total 50 > height 10 → overflow
	cv.vp.SetContent(content)
	cv.plainLines = plainLines(content)
	return Model{
		width:        w,
		height:       vh + 6,
		scrollbarTop: 2, // header(1) + divider(1), no splash
		chat:         cv,
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
	return m.chat.YOffset()
}

// PROBE: verify the ScrollbarHit threshold maps the four canonical X values
// correctly at the test fixture's dimensions (width=80, contentW=79, vp.Width=79).
//
// The bar is painted at screen column 79 (width-1=79). The viewport text width
// is 79 (contentW-2 = 80-2=78... wait, buildDragModel sets vpWidth=w-1=79).
// So c.Width()=79: bar is at col 79, text is cols 0..78.
// Expected: ScrollbarHit(79,·)=true, ScrollbarHit(80,·)=true (off-by-one terminal),
//
//	ScrollbarHit(78,·)=false (last text col), ScrollbarHit(5,·)=false (text col).
func TestScrollbarHitProbe(t *testing.T) {
	m := buildDragModel()
	cv := &m.chat
	// cv.Width() == 79 (vpWidth passed to newChatView is w-1 = 79)
	if w := cv.Width(); w != 79 {
		t.Fatalf("PROBE: expected c.Width()=79, got %d (fixture changed?)", w)
	}
	// Bar column (79) must hit.
	if !cv.ScrollbarHit(79, 5) {
		t.Error("PROBE: ScrollbarHit(79,5) want true (bar col), got false")
	}
	// Off-by-one (80) must also hit.
	if !cv.ScrollbarHit(80, 5) {
		t.Error("PROBE: ScrollbarHit(80,5) want true (terminal off-by-one), got false")
	}
	// Last text col (78) must NOT hit.
	if cv.ScrollbarHit(78, 5) {
		t.Error("PROBE: ScrollbarHit(78,5) want false (text col), got true — threshold too low")
	}
	// Typical text col (5) must NOT hit.
	if cv.ScrollbarHit(5, 5) {
		t.Error("PROBE: ScrollbarHit(5,5) want false (text col), got true")
	}
	// Text col (2) must NOT hit.
	if cv.ScrollbarHit(2, 5) {
		t.Error("PROBE: ScrollbarHit(2,5) want false (text col), got true")
	}
}

// Regression (root cause from live trace): a click in the rightmost column is
// reported by some terminals as X=width — one past the bar at width-1. The grab
// must still register and the drag must scroll.
func TestScrollbarGrabAtRightEdge(t *testing.T) {
	m := buildDragModel() // width 80 → bar at column 79
	m = send(t, m, tea.MouseClickMsg{X: 80, Y: 2, Button: tea.MouseLeft})  // X == width (off by one)
	m = send(t, m, tea.MouseMotionMsg{X: 80, Y: 9, Button: tea.MouseLeft}) // drag down
	if got := m.chat.YOffset(); got == 0 {
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
	if !m.chat.SelectionDragging() {
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
		m.chat.SelectionDragging(), m.chat.SelectionActive(), m.chat.ScrollbarDragging())
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
