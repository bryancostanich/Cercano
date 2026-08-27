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
	cv := newChatView(theme.NewStyles(p), p, "", "", vpWidth, vpHeight)
	if content != "" {
		seedChatViewLines(&cv, strings.Split(content, "\n"))
	}
	if plainLns != nil {
		cv.plainLines = plainLns
	}
	m := Model{}
	m.setMainChat(cv)
	return m
}

func TestSelectedTextSingleLine(t *testing.T) {
	m := newSelectionModel(0, 0, "", []string{"hello world"})
	m.mainChat().selection = textSelection{
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

	if got, want := m.mainChat().selectedText(), "world"; got != want {
		t.Fatalf("selectedText() = %q, want %q", got, want)
	}
}

func TestSelectedTextMultilineReverseDrag(t *testing.T) {
	m := newSelectionModel(0, 0, "", []string{"first line", "second", "third"})
	m.mainChat().selection = textSelection{
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

	if got, want := m.mainChat().selectedText(), "line\nsecond\nth"; got != want {
		t.Fatalf("selectedText() = %q, want %q", got, want)
	}
}

func TestSelectionPointFromMouseUsesViewportOffset(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line"
	}
	p := theme.Cracker()
	cv := newChatView(theme.NewStyles(p), p, "", "", 20, 4)
	seedChatViewLines(&cv, lines)
	cv.SetYOffset(10)
	cv.plainLines = lines

	// scrollbarTop=2, screen mouse.Y=4 → local Y = 4-2 = 2
	// YOffset=10 + localY=2 → line 12. X=3 → col 3.
	got := cv.selectionPointFromLocal(3, 4-2, false)
	want := selectionPoint{Line: 12, Col: 3}
	if got != want {
		t.Fatalf("selectionPointFromLocal() = %#v, want %#v", got, want)
	}
}

func TestHeaderTitleDragSelectionCopiesText(t *testing.T) {
	p := theme.Cracker()
	m := Model{
		styles:       theme.NewStyles(p),
		palette:      p,
		width:        80,
		sessionTitle: "Selectable Title",
	}
	start, end, ok := m.headerTitleRange()
	if !ok {
		t.Fatal("header title range missing")
	}

	next, _ := m.Update(tea.MouseClickMsg{X: start, Y: 0, Button: tea.MouseLeft})
	m = next.(Model)
	if !m.headerSelection.Dragging {
		t.Fatal("header title mouse down should start a header selection drag")
	}
	next, _ = m.Update(tea.MouseMotionMsg{X: end, Y: 0, Button: tea.MouseLeft})
	m = next.(Model)
	if got := m.selectedHeaderText(); got != "Selectable Title" {
		t.Fatalf("selectedHeaderText() = %q, want title", got)
	}
	next, cmd := m.Update(tea.MouseReleaseMsg{X: end, Y: 0, Button: tea.MouseLeft})
	if cmd == nil {
		t.Fatal("header title mouse release should copy non-empty selection")
	}
	m = next.(Model)
	if m.headerSelection.Dragging {
		t.Fatal("header selection drag should stop on release")
	}
	if m.selectionNotice != "copied selection" {
		t.Fatalf("selectionNotice = %q, want copied selection", m.selectionNotice)
	}
}

func TestMouseReleaseCopiesDragSelection(t *testing.T) {
	p := theme.Cracker()
	cv := newChatView(theme.NewStyles(p), p, "", "", 20, 4)
	seedChatViewLines(&cv, []string{"hello world"})
	cv.plainLines = []string{"hello world"}
	cv.selection = textSelection{Active: true, Dragging: true, Anchor: selectionPoint{Line: 0, Col: 0}, Cursor: selectionPoint{Line: 0, Col: 1}}

	cv.scrollbarDragging = true
	m := Model{
		scrollbarTop:    0,
		selectionNotice: "",
	}
	m.setMainChat(cv)

	next, cmd := m.Update(tea.MouseReleaseMsg{X: 5, Y: 0, Button: tea.MouseLeft})
	if cmd == nil {
		t.Fatal("MouseRelease should return clipboard command for non-empty selection")
	}
	got := next.(Model)
	if got.selectionNotice != "copied selection" {
		t.Fatalf("selectionNotice = %q, want copied selection", got.selectionNotice)
	}
	if got.mainChat().ScrollbarDragging() {
		t.Fatal("scrollbarDragging should be cleared")
	}
	if !got.mainChat().SelectionHasRange() {
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
	m.mainChat().focusedToolIdx = 1
	m.mainChat().selection = textSelection{
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
	if got.mainChat().SelectionActive() {
		t.Fatal("paste should clear active viewport selection")
	}
	if got.selectionNotice != "" {
		t.Fatalf("selectionNotice = %q, want cleared notice", got.selectionNotice)
	}
	if got.mainChat().InToolNav() {
		t.Fatalf("paste should exit tool-nav mode (focusedToolIdx should be -1)")
	}
}

func TestRenderSelectionOnLinePreservesPlainText(t *testing.T) {
	p := theme.Cracker()
	cv := newChatView(theme.NewStyles(p), p, "", "", 10, 3)
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
