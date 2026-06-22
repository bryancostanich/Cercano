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

	wheel := func(btn ansi.MouseButton, y int) {
		m = send(t, m, tea.MouseWheelMsg{Button: btn, Y: y})
	}

	// Over the input rows → chat must NOT move (wheel drives the textarea).
	m.viewport.GotoBottom()
	chatBefore := m.viewport.YOffset()
	wheel(ansi.MouseWheelUp, m.inputTop)
	if m.viewport.YOffset() != chatBefore {
		t.Fatalf("wheel over the input scrolled the chat: %d -> %d", chatBefore, m.viewport.YOffset())
	}

	// Over the divider line just above the input → chat scrolls.
	m.viewport.GotoBottom()
	chatBefore = m.viewport.YOffset()
	wheel(ansi.MouseWheelUp, m.scrollbarTop+m.viewport.Height())
	if m.viewport.YOffset() == chatBefore {
		t.Fatalf("wheel over the divider did not scroll the chat (stayed %d)", chatBefore)
	}

	// Over the viewport → chat scrolls.
	m.viewport.GotoBottom()
	chatBefore = m.viewport.YOffset()
	wheel(ansi.MouseWheelUp, m.scrollbarTop+1)
	if m.viewport.YOffset() == chatBefore {
		t.Fatalf("wheel over the viewport did not scroll the chat (stayed %d)", chatBefore)
	}
}

// Wheel over the multi-line prompt scrolls it both ways, and scrolling down
// stops at the last content line — never into empty space.
func TestPromptWheel_ScrollsBothWaysAndClamps(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	// 10 content lines; prompt caps at maxInputLines and scrolls.
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = "l" + string(rune('0'+i))
	}
	m.input.SetValue(strings.Join(lines, "\n"))
	m.relayout()
	last := m.input.LineCount() - 1 // cursor starts at end

	if m.input.Line() != last {
		t.Fatalf("precondition: cursor at line %d, want %d", m.input.Line(), last)
	}

	// Wheel up moves toward the top.
	for i := 0; i < 3; i++ {
		m = send(t, m, tea.MouseWheelMsg{Button: ansi.MouseWheelUp, Y: m.inputTop})
	}
	if got := m.input.Line(); got != last-3 {
		t.Fatalf("after 3 wheel-up, line = %d, want %d", got, last-3)
	}

	// Wheel down past the end clamps at the last line — no empty overscroll.
	for i := 0; i < 20; i++ {
		m = send(t, m, tea.MouseWheelMsg{Button: ansi.MouseWheelDown, Y: m.inputTop})
	}
	if got := m.input.Line(); got != last {
		t.Fatalf("after wheel-down past end, line = %d, want clamp at %d", got, last)
	}
}
