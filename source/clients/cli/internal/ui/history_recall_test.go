package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func pressUp(t *testing.T, m Model) Model {
	t.Helper()
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	return next.(Model)
}

func pressDown(t *testing.T, m Model) Model {
	t.Helper()
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	return next.(Model)
}

func TestHistoryRecall_UpDownCyclesSubmittedInputs(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	// Seed history directly (submit() needs a live agent for messages).
	m.inputHistory = []string{"first", "second", "third"}
	m.historyIdx = len(m.inputHistory)

	// ↑ recalls newest → oldest.
	m = pressUp(t, m)
	if m.input.Value() != "third" {
		t.Fatalf("1st up = %q, want third", m.input.Value())
	}
	m = pressUp(t, m)
	if m.input.Value() != "second" {
		t.Fatalf("2nd up = %q, want second", m.input.Value())
	}
	m = pressUp(t, m)
	if m.input.Value() != "first" {
		t.Fatalf("3rd up = %q, want first", m.input.Value())
	}
	// Past the oldest stays put.
	m = pressUp(t, m)
	if m.input.Value() != "first" {
		t.Fatalf("up past oldest = %q, want first", m.input.Value())
	}

	// ↓ steps back toward newer, then to the (empty) live input.
	m = pressDown(t, m)
	if m.input.Value() != "second" {
		t.Fatalf("1st down = %q, want second", m.input.Value())
	}
	m = pressDown(t, m)
	m = pressDown(t, m)
	if m.input.Value() != "" {
		t.Fatalf("down to live input = %q, want empty (stash)", m.input.Value())
	}
}

func TestHistoryRecall_UpMovesCursorWhenNotOnFirstLine(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.inputHistory = []string{"old"}
	m.historyIdx = 1
	// A two-line draft with the cursor on the last line.
	m.input.SetValue("line one\nline two")
	m.relayout()

	// Cursor is on line 1 (the last) → ↑ should move the cursor, not recall.
	if m.input.Line() == 0 {
		t.Fatalf("precondition: cursor should be on the second line")
	}
	m = pressUp(t, m)
	if m.input.Value() != "line one\nline two" {
		t.Fatalf("up on a non-first line recalled history instead of moving cursor: %q", m.input.Value())
	}
}

func TestHistoryRecall_UpMovesCursorWithinSoftWrappedLine(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.inputHistory = []string{"old"}
	m.historyIdx = 1
	// A single logical line (no '\n') long enough to soft-wrap across several
	// visual rows, cursor left at the end (on the last visual row). This is the
	// case Line() misjudged: it counts hard newlines only, so it reads 0 for the
	// whole wrapped span and ↑ used to clobber the draft with history.
	draft := strings.Repeat("wrap ", 60)
	m.input.SetValue(draft)
	m.relayout()

	if m.input.Line() != 0 {
		t.Fatalf("precondition: a newline-free draft should be logical line 0, got %d", m.input.Line())
	}
	if m.input.CursorOnFirstRow() {
		t.Fatalf("precondition: cursor at end of a soft-wrapped draft should not be on the first visual row")
	}
	m = pressUp(t, m)
	if m.input.Value() != draft {
		t.Fatalf("up inside a soft-wrapped line recalled history instead of moving cursor: %q", m.input.Value())
	}

	// From the top visual row, ↑ recalls history as usual.
	m.input.CursorStart()
	if !m.input.CursorOnFirstRow() {
		t.Fatalf("precondition: CursorStart should land on the first visual row")
	}
	m = pressUp(t, m)
	if m.input.Value() != "old" {
		t.Fatalf("up on the first visual row = %q, want history recall %q", m.input.Value(), "old")
	}
}
