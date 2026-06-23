package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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

func TestPrompt_WrapsAndCapsHeight(t *testing.T) {
	p := newTestPromptInput(8) // 2 prompt cols + 6 text cols
	p.SetValue("abcdefghijkl")
	if got, want := p.Height(), 2; got != want {
		t.Fatalf("wrapped height = %d, want %d", got, want)
	}

	p.SetValue(strings.Repeat("x\n", 20))
	if got := p.Height(); got != maxInputLines {
		t.Fatalf("capped height = %d, want %d", got, maxInputLines)
	}
}

func TestPrompt_WheelOverPromptScrollsWithoutMovingCursor(t *testing.T) {
	m := New(nil, false)
	m.width = 80
	m.height = 24
	m.input.SetValue(strings.Join([]string{
		"line 0", "line 1", "line 2", "line 3",
		"line 4", "line 5", "line 6", "line 7",
		"line 8", "line 9",
	}, "\n"))
	m.relayout()

	cursor := m.input.cursor
	maxScroll := m.input.MaxScrollYOffset()
	if maxScroll == 0 {
		t.Fatal("test setup expected scrollable prompt")
	}

	top := m.promptTop()
	next, _ := m.Update(tea.MouseWheelMsg{X: 2, Y: top, Button: tea.MouseWheelUp})
	m = next.(Model)
	if m.input.cursor != cursor {
		t.Fatalf("cursor moved on wheel: got %d want %d", m.input.cursor, cursor)
	}
	if got := m.input.ScrollYOffset(); got >= maxScroll {
		t.Fatalf("wheel up did not scroll prompt upward: got %d, max was %d", got, maxScroll)
	}

	for range 10 {
		next, _ = m.Update(tea.MouseWheelMsg{X: 2, Y: top, Button: tea.MouseWheelDown})
		m = next.(Model)
	}
	if got := m.input.ScrollYOffset(); got != maxScroll {
		t.Fatalf("wheel down should clamp at max scroll: got %d want %d", got, maxScroll)
	}
	if m.input.cursor != cursor {
		t.Fatalf("cursor moved after wheel down: got %d want %d", m.input.cursor, cursor)
	}
}

func TestPrompt_WheelOutsidePromptScrollsChatViewport(t *testing.T) {
	m := New(nil, false)
	m.width = 80
	m.height = 24
	m.relayout()
	m.viewport.SetContent(strings.Repeat("chat\n", 80))
	m.viewport.SetYOffset(0)

	next, _ := m.Update(tea.MouseWheelMsg{X: 2, Y: m.scrollbarTop, Button: tea.MouseWheelDown})
	got := next.(Model)
	if got.viewport.YOffset() == 0 {
		t.Fatal("wheel outside prompt should scroll chat viewport")
	}
}

func TestPrompt_MacNavigationAndSelection(t *testing.T) {
	p := newTestPromptInput(80)
	p.SetValue("hello brave world\nnext line")

	p, _ = updatePromptKey(p, tea.KeyLeft, "", tea.ModAlt)
	if got, want := p.cursor, len([]rune("hello brave world\nnext ")); got != want {
		t.Fatalf("option-left cursor = %d, want %d", got, want)
	}

	p, _ = updatePromptKey(p, tea.KeyLeft, "", tea.ModSuper)
	if got, want := p.cursor, len([]rune("hello brave world\n")); got != want {
		t.Fatalf("cmd-left cursor = %d, want %d", got, want)
	}

	p, _ = updatePromptKey(p, tea.KeyUp, "", tea.ModSuper|tea.ModShift)
	if got := p.selectedText(); got != "hello brave world\n" {
		t.Fatalf("shift-cmd-up selected %q", got)
	}
}

func TestPrompt_SelectionTypingReplacesSelection(t *testing.T) {
	p := newTestPromptInput(80)
	p.SetValue("hello")
	p, _ = updatePromptRune(p, 'a', tea.ModSuper)
	if got := p.selectedText(); got != "hello" {
		t.Fatalf("cmd+a selected %q, want hello", got)
	}

	p, _ = updatePromptText(p, "x")
	if got := p.Value(); got != "x" {
		t.Fatalf("typing over selection = %q, want x", got)
	}
}

func TestPrompt_MouseDragSelection(t *testing.T) {
	p := newTestPromptInput(80)
	p.SetValue("hello")
	p.MouseDown(2, 0)
	p.MouseDrag(5, 0)
	p.MouseUp(5, 0)
	if got := p.selectedText(); got != "hel" {
		t.Fatalf("mouse selected %q, want hel", got)
	}
}

func TestPrompt_UndoRedoPasteSingleStep(t *testing.T) {
	p := newTestPromptInput(80)
	p, _ = p.Update(tea.PasteMsg{Content: "one\ntwo\nthree"})
	if got := p.Value(); got != "one\ntwo\nthree" {
		t.Fatalf("paste value = %q", got)
	}

	p, _ = updatePromptRune(p, 'z', tea.ModSuper)
	if got := p.Value(); got != "" {
		t.Fatalf("cmd+z after paste = %q, want empty", got)
	}

	p, _ = updatePromptRune(p, 'z', tea.ModSuper|tea.ModShift)
	if got := p.Value(); got != "one\ntwo\nthree" {
		t.Fatalf("cmd+shift+z = %q, want pasted text", got)
	}
}

func TestPrompt_UndoCoalescesTyping(t *testing.T) {
	p := newTestPromptInput(80)
	for _, r := range "abc" {
		p, _ = updatePromptText(p, string(r))
	}
	if got := p.Value(); got != "abc" {
		t.Fatalf("typed value = %q, want abc", got)
	}

	p, _ = updatePromptRune(p, 'z', tea.ModSuper)
	if got := p.Value(); got != "" {
		t.Fatalf("cmd+z should undo coalesced typing, got %q", got)
	}
}

func TestPrompt_BackspaceDeletesSelection(t *testing.T) {
	p := newTestPromptInput(80)
	p.SetValue("hello")
	p.selectionAnchor = 1
	p.cursor = 4

	p, _ = updatePromptKey(p, tea.KeyBackspace, "", 0)
	if got := p.Value(); got != "ho" {
		t.Fatalf("backspace over selection = %q, want ho", got)
	}
}

func TestPrompt_ViewHasNoScrollableBlankPadding(t *testing.T) {
	p := newTestPromptInput(80)
	p.SetValue(strings.Repeat("x\n", 10))
	p.ScrollView(999)
	if got, want := p.ScrollYOffset(), p.MaxScrollYOffset(); got != want {
		t.Fatalf("scroll offset = %d, want max %d", got, want)
	}
	if strings.Contains(ansi.Strip(p.View()), "\n  \n") {
		t.Fatalf("view contains blank prompt padding rows:\n%s", ansi.Strip(p.View()))
	}
}

func newTestPromptInput(width int) promptInput {
	p := newPromptInput()
	p.Placeholder = "placeholder"
	p.SetPromptFunc(2, func(info promptInfo) string {
		if info.LineNumber == 0 {
			return "▶ "
		}
		return "  "
	})
	p.Focus()
	p.SetWidth(width)
	return p
}

func updatePromptText(p promptInput, text string) (promptInput, tea.Cmd) {
	r := []rune(text)
	code := rune(0)
	if len(r) == 1 {
		code = r[0]
	}
	return p.Update(tea.KeyPressMsg{Code: code, Text: text})
}

func updatePromptRune(p promptInput, r rune, mod tea.KeyMod) (promptInput, tea.Cmd) {
	return p.Update(tea.KeyPressMsg{Code: r, BaseCode: unicodeLower(r), Mod: mod})
}

func updatePromptKey(p promptInput, code rune, text string, mod tea.KeyMod) (promptInput, tea.Cmd) {
	return p.Update(tea.KeyPressMsg{Code: code, Text: text, Mod: mod})
}

func unicodeLower(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}
