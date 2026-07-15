package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// dragSelectPrompt selects "hello" in the prompt via a mouse drag across the
// first prompt row and returns the model after release. It does NOT press any
// copy key — release alone must copy.
func dragSelectPrompt(t *testing.T) (Model, tea.Cmd) {
	t.Helper()
	m := New(nil, false)
	m.width = 80
	m.height = 24
	m.relayout()
	m.input.Focus()
	m.input.SetValue("hello world")

	top := m.promptTop()
	next, _ := m.Update(tea.MouseClickMsg{X: 2, Y: top, Button: tea.MouseLeft})
	m = next.(Model)
	next, _ = m.Update(tea.MouseMotionMsg{X: 7, Y: top, Button: tea.MouseLeft})
	m = next.(Model)
	next, cmd := m.Update(tea.MouseReleaseMsg{X: 7, Y: top, Button: tea.MouseLeft})
	return next.(Model), cmd
}

// Releasing a drag-selection in the prompt must auto-copy the selected text to
// the clipboard — mirroring the scrollback viewport. This is the only copy path
// that survives terminals which reserve Cmd+C for their own copy and never
// deliver the keypress to the app, so without it "select in the prompt" appears
// to do nothing.
func TestPromptMouseReleaseAutoCopiesSelection(t *testing.T) {
	m, cmd := dragSelectPrompt(t)

	if cmd == nil {
		t.Fatal("releasing a prompt drag returned no clipboard command")
	}
	if m.selectionNotice != "copied selection" {
		t.Fatalf("selectionNotice = %q, want %q", m.selectionNotice, "copied selection")
	}
	// The selection stays visible after auto-copy (same as scrollback), so the
	// user can see what was copied.
	if !m.input.HasSelection() {
		t.Fatal("selection should remain visible after auto-copy")
	}
}

// A drag that collapses to a caret (no range) must NOT emit a clipboard command
// or a copy notice — releasing a plain click just repositions the cursor.
func TestPromptMouseReleaseWithoutRangeDoesNotCopy(t *testing.T) {
	m := New(nil, false)
	m.width = 80
	m.height = 24
	m.relayout()
	m.input.Focus()
	m.input.SetValue("hello world")

	top := m.promptTop()
	next, _ := m.Update(tea.MouseClickMsg{X: 3, Y: top, Button: tea.MouseLeft})
	m = next.(Model)
	// Release at the same column — no range selected.
	next, cmd := m.Update(tea.MouseReleaseMsg{X: 3, Y: top, Button: tea.MouseLeft})
	m = next.(Model)

	if cmd != nil {
		t.Fatal("a zero-width prompt drag must not emit a clipboard command")
	}
	if m.selectionNotice != "" {
		t.Fatalf("selectionNotice = %q, want empty", m.selectionNotice)
	}
}

// MouseUp returns the selected text so the host can copy it; a collapsed drag
// returns empty.
func TestPromptInputMouseUpReturnsSelectedText(t *testing.T) {
	p := newPromptInput()
	p.SetWidth(40)
	p.Focus()
	p.SetValue("hello world")

	p.MouseDown(0, 0)
	p.MouseDrag(5, 0)
	if got := p.MouseUp(5, 0); got != "hello" {
		t.Fatalf("MouseUp returned %q, want %q", got, "hello")
	}

	// A collapsed drag (down and up at the same point) returns empty.
	p.MouseDown(0, 0)
	if got := p.MouseUp(0, 0); got != "" {
		t.Fatalf("collapsed MouseUp returned %q, want empty", got)
	}
}
