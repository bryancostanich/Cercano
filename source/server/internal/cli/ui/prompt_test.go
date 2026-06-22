package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPrompt_HeightGrowsWithLinesAndCaps(t *testing.T) {
	m := New(nil, false)
	m.width = 80
	m.height = 24
	m.relayout()
	if got := m.input.Height(); got != 1 {
		t.Fatalf("empty prompt height = %d, want 1", got)
	}

	m.input.SetValue("a\nb\nc")
	m.relayout()
	if got := m.input.Height(); got != 3 {
		t.Fatalf("3-line prompt height = %d, want 3", got)
	}

	m.input.SetValue(strings.Repeat("x\n", 20))
	m.relayout()
	if got := m.input.Height(); got != maxInputLines {
		t.Fatalf("many-line prompt height = %d, want cap %d", got, maxInputLines)
	}
}

func TestPrompt_ShiftEnterInsertsNewline(t *testing.T) {
	m := New(nil, false)
	m.width = 80
	m.height = 24
	m.relayout()

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	got := next.(Model)

	if got.input.Value() != "\n" {
		t.Fatalf("shift+enter value = %q, want a single newline", got.input.Value())
	}
	if h := got.input.Height(); h != 2 {
		t.Fatalf("height after shift+enter = %d, want 2", h)
	}
}
