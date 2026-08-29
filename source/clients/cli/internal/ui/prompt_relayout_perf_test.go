package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPromptTypingSameShapeDoesNotRebuildChatLayout(t *testing.T) {
	m := New(nil, false)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 126, Height: 47})
	m = next.(Model)

	entries := make([]*Entry, 0, 20)
	for i := 0; i < cap(entries); i++ {
		entries = append(entries, &Entry{Role: RoleAssistant, Content: fmt.Sprintf("entry %d\n", i) + strings.Repeat("history line ", 20)})
	}
	m.mainChat().SetEntries(entries)
	if len(m.mainChat().layout.units) == 0 {
		t.Fatal("test setup expected transcript layout units")
	}
	firstUnit := &m.mainChat().layout.units[0]

	next, _ = m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = next.(Model)

	if got := m.input.Value(); got != "x" {
		t.Fatalf("input value = %q, want typed key", got)
	}
	if len(m.mainChat().layout.units) == 0 || &m.mainChat().layout.units[0] != firstUnit {
		t.Fatal("same-shape prompt typing rebuilt chat layout")
	}
}
