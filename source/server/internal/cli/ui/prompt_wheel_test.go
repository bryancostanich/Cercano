package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// With a multi-line prompt, a wheel event over the bottom prompt region scrolls
// the textarea (leaving the chat scroll alone); over the viewport it scrolls the
// chat as before.
func TestPromptWheel_RoutesByPointerRegion(t *testing.T) {
	m := New(nil, false)
	// Overflowing chat content so the viewport is scrollable.
	m = m.SeedAssistantMarkdown(strings.Repeat("a line of chat scrollback\n\n", 40))
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	// Grow the prompt to multiple lines.
	m.input.SetValue(strings.Repeat("x\n", 8))
	m.relayout()
	if m.input.Height() <= 1 {
		t.Fatalf("precondition: expected multi-line prompt, height=%d", m.input.Height())
	}

	// Wheel over the input region (below the viewport) must NOT move the chat.
	before := m.viewport.YOffset()
	inputY := m.scrollbarTop + m.viewport.Height() + 1
	m = send(t, m, tea.MouseWheelMsg{Button: ansi.MouseWheelUp, Y: inputY})
	if m.viewport.YOffset() != before {
		t.Fatalf("wheel over prompt scrolled the chat: %d -> %d", before, m.viewport.YOffset())
	}

	// Wheel over the viewport DOES move the chat.
	viewY := m.scrollbarTop + 1
	m = send(t, m, tea.MouseWheelMsg{Button: ansi.MouseWheelUp, Y: viewY})
	if m.viewport.YOffset() == before {
		t.Fatalf("wheel over viewport did not scroll the chat (stayed %d)", before)
	}
}
