package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// With a multi-line prompt, a wheel event is fed to the textarea ONLY when the
// pointer is over the input's rows. Over the chat viewport — or the chrome
// between them (the divider line above the input) — it scrolls the chat.
func TestPromptWheel_RoutesByPointerRegion(t *testing.T) {
	m := New(nil, false)
	m = m.SeedAssistantMarkdown(strings.Repeat("a line of chat scrollback\n\n", 40))
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m.input.SetValue(strings.Repeat("x\n", 8))
	m.relayout()
	if m.input.Height() <= 1 {
		t.Fatalf("precondition: expected multi-line prompt, height=%d", m.input.Height())
	}

	wheelAt := func(y int) (before, after int) {
		m.viewport.GotoBottom()
		before = m.viewport.YOffset()
		m = send(t, m, tea.MouseWheelMsg{Button: ansi.MouseWheelUp, Y: y})
		return before, m.viewport.YOffset()
	}

	// Over the input rows → chat must NOT move (wheel goes to the textarea).
	if b, a := wheelAt(m.inputTop); a != b {
		t.Fatalf("wheel over the input scrolled the chat: %d -> %d", b, a)
	}

	// Over the divider line just above the input (below the viewport, NOT an
	// input row) → chat scrolls.
	dividerY := m.scrollbarTop + m.viewport.Height()
	if b, a := wheelAt(dividerY); a == b {
		t.Fatalf("wheel over the divider did not scroll the chat (stayed %d)", b)
	}

	// Over the viewport → chat scrolls.
	if b, a := wheelAt(m.scrollbarTop + 1); a == b {
		t.Fatalf("wheel over the viewport did not scroll the chat (stayed %d)", b)
	}
}
