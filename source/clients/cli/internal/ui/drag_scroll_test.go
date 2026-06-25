package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Dragging a selection past the top edge must keep scrolling on each tick even
// when the pointer doesn't move — the bug was that scroll only happened on
// mouse-motion events.
func TestDragScroll_ContinuesWhileHeldAtEdge(t *testing.T) {
	m := New(nil, false)
	m = m.SeedAssistantMarkdown(strings.Repeat("scrollback line\n\n", 60))
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m.chat.SetYOffset(m.chat.TotalLineCount()) // bottom
	startOffset := m.chat.YOffset()
	if startOffset == 0 {
		t.Fatalf("precondition: content should overflow so it can scroll")
	}

	// Begin a drag inside the viewport, then "move" the pointer above the top
	// edge and hold it there (one motion event, then ticks with no motion).
	m.chat.selection = textSelection{Active: true, Dragging: true,
		Anchor: selectionPoint{Line: startOffset, Col: 0}}
	aboveTop := tea.Mouse{X: 5, Y: m.scrollbarTop - 1}

	next, cmd := m.Update(tea.MouseMotionMsg(aboveTop))
	m = next.(Model)
	if !m.chat.DragScrolling() || cmd == nil {
		t.Fatalf("expected edge auto-scroll to start (dragScrolling=%v, cmd=%v)", m.chat.DragScrolling(), cmd != nil)
	}
	afterMotion := m.chat.YOffset()
	if afterMotion >= startOffset {
		t.Fatalf("motion at the top edge should scroll up: %d -> %d", startOffset, afterMotion)
	}

	// Ticks with NO further motion must keep scrolling up.
	for i := 0; i < 3; i++ {
		next, cmd = m.Update(dragScrollTickMsg{})
		m = next.(Model)
		if cmd == nil {
			t.Fatalf("tick %d: expected the auto-scroll to reschedule", i)
		}
	}
	if m.chat.YOffset() >= afterMotion {
		t.Fatalf("ticks did not keep scrolling: %d -> %d", afterMotion, m.chat.YOffset())
	}

	// Releasing stops the loop.
	next, _ = m.Update(tea.MouseReleaseMsg(tea.Mouse{X: 5, Y: m.scrollbarTop - 1}))
	m = next.(Model)
	if m.chat.DragScrolling() {
		t.Fatalf("release should stop the auto-scroll loop")
	}
	next, cmd = m.Update(dragScrollTickMsg{})
	m = next.(Model)
	if cmd != nil {
		t.Fatalf("a tick after release should not reschedule")
	}
}
