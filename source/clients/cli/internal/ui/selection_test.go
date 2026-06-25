package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
)

// newSelectionModel builds a minimal Model for selection tests. It sets up a
// chatView sized to vpWidth×vpHeight and pre-seeds plain lines for copy tests.
func newSelectionModel(vpWidth, vpHeight int, content string, plainLns []string) Model {
	p := theme.Cracker()
	cv := newChatView(theme.NewStyles(p), p, vpWidth, vpHeight)
	if content != "" {
		cv.vp.SetContent(content)
	}
	if plainLns != nil {
		cv.plainLines = plainLns
	}
	return Model{chat: cv}
}

func TestSelectedTextSingleLine(t *testing.T) {
	m := newSelectionModel(0, 0, "", []string{"hello world"})
	m.chat.selection = textSelection{
		Active: true,
		Anchor: selectionPoint{
			Line: 0,
			Col:  6,
		},
		Cursor: selectionPoint{
			Line: 0,
			Col:  11,
		},
	}

	if got, want := m.chat.selectedText(), "world"; got != want {
		t.Fatalf("selectedText() = %q, want %q", got, want)
	}
}

func TestSelectedTextMultilineReverseDrag(t *testing.T) {
	m := newSelectionModel(0, 0, "", []string{"first line", "second", "third"})
	m.chat.selection = textSelection{
		Active: true,
		Anchor: selectionPoint{
			Line: 2,
			Col:  2,
		},
		Cursor: selectionPoint{
			Line: 0,
			Col:  6,
		},
	}

	if got, want := m.chat.selectedText(), "line\nsecond\nth"; got != want {
		t.Fatalf("selectedText() = %q, want %q", got, want)
	}
}

func TestSelectionPointFromMouseUsesViewportOffset(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line"
	}
	p := theme.Cracker()
	cv := newChatView(theme.NewStyles(p), p, 20, 4)
	cv.vp.SetContent(strings.Join(lines, "\n"))
	cv.vp.SetYOffset(10)
	cv.plainLines = lines

	m := Model{
		scrollbarTop: 2,
		chat:         cv,
	}

	got := m.selectionPointFromMouse(tea.Mouse{X: 3, Y: 4}, false)
	want := selectionPoint{Line: 12, Col: 3}
	if got != want {
		t.Fatalf("selectionPointFromMouse() = %#v, want %#v", got, want)
	}
}

func TestMouseReleaseCopiesDragSelection(t *testing.T) {
	p := theme.Cracker()
	cv := newChatView(theme.NewStyles(p), p, 20, 4)
	cv.vp.SetContent("hello world")
	cv.plainLines = []string{"hello world"}
	cv.selection = textSelection{Active: true, Dragging: true, Anchor: selectionPoint{Line: 0, Col: 0}, Cursor: selectionPoint{Line: 0, Col: 1}}

	m := Model{
		scrollbarTop:      0,
		chat:              cv,
		selectionNotice:   "",
		scrollbarDragging: true,
	}

	next, cmd := m.Update(tea.MouseReleaseMsg{X: 5, Y: 0, Button: tea.MouseLeft})
	if cmd == nil {
		t.Fatal("MouseRelease should return clipboard command for non-empty selection")
	}
	got := next.(Model)
	if got.selectionNotice != "copied selection" {
		t.Fatalf("selectionNotice = %q, want copied selection", got.selectionNotice)
	}
	if got.scrollbarDragging {
		t.Fatal("scrollbarDragging should be cleared")
	}
	if !got.chat.SelectionHasRange() {
		t.Fatal("selection should remain visible after auto-copy")
	}
}

func TestPasteMsgInsertsIntoPrompt(t *testing.T) {
	m := New(nil, false)
	m.width = 80
	m.height = 24
	m.relayout()

	next, _ := m.Update(tea.PasteMsg{Content: "hello\nworld"})
	got := next.(Model)

	// The prompt is a multi-line textarea now, so a pasted newline is preserved.
	if got.input.Value() != "hello\nworld" {
		t.Fatalf("input.Value() = %q, want pasted text inserted verbatim", got.input.Value())
	}
}

func TestPasteMsgClearsSelectionAndToolFocus(t *testing.T) {
	m := New(nil, false)
	m.width = 80
	m.height = 24
	m.focusedToolIdx = 1
	m.chat.selection = textSelection{
		Active: true,
		Anchor: selectionPoint{Line: 0, Col: 0},
		Cursor: selectionPoint{Line: 0, Col: 2},
	}
	m.selectionNotice = "copied selection"
	m.relayout()

	next, _ := m.Update(tea.PasteMsg{Content: "pasted"})
	got := next.(Model)

	if got.input.Value() != "pasted" {
		t.Fatalf("input.Value() = %q, want pasted text", got.input.Value())
	}
	if got.chat.SelectionActive() {
		t.Fatal("paste should clear active viewport selection")
	}
	if got.selectionNotice != "" {
		t.Fatalf("selectionNotice = %q, want cleared notice", got.selectionNotice)
	}
	if got.focusedToolIdx != -1 {
		t.Fatalf("focusedToolIdx = %d, want -1", got.focusedToolIdx)
	}
}

func TestRenderSelectionOnLinePreservesPlainText(t *testing.T) {
	p := theme.Cracker()
	cv := newChatView(theme.NewStyles(p), p, 10, 3)
	cv.selection = textSelection{
		Active: true,
		Anchor: selectionPoint{Line: 0, Col: 2},
		Cursor: selectionPoint{Line: 0, Col: 5},
	}

	line := lipgloss.NewStyle().Foreground(p.Primary).Render("0123456789")
	got := cv.renderSelectionOnLine(line, 0)
	if stripped := lipgloss.Width(got); stripped != 10 {
		t.Fatalf("renderSelectionOnLine width = %d, want 10", stripped)
	}
}

func TestIsSelectionCopyKeyRecognizesCommandC(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.KeyPressMsg
		want bool
	}{
		{
			name: "super c",
			msg:  tea.KeyPressMsg{Code: 'c', Mod: tea.ModSuper},
			want: true,
		},
		{
			name: "meta c compatibility",
			msg:  tea.KeyPressMsg{Code: 'c', Mod: tea.ModMeta},
			want: true,
		},
		{
			name: "keyboard protocol base code",
			msg:  tea.KeyPressMsg{Code: 'ç', BaseCode: 'c', Mod: tea.ModSuper},
			want: true,
		},
		{
			name: "plain c handled elsewhere",
			msg:  tea.KeyPressMsg{Code: 'c'},
			want: false,
		},
		{
			name: "alt c is not command",
			msg:  tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSelectionCopyKey(tt.msg); got != tt.want {
				t.Fatalf("isSelectionCopyKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

