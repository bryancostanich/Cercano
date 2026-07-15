package ui

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	// A Kitty-capable terminal can report Shift+Enter two ways: as a bare
	// chord, or — because we enable ReportAssociatedText — with Text "\n"
	// attached. The latter makes msg.String() return "\n" instead of
	// "shift+enter", so the routing must recognize it structurally. Both forms
	// must compose a newline, never submit.
	forms := []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{"chord", tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}},
		{"associated text", tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift, Text: "\n"}},
	}
	for _, f := range forms {
		t.Run(f.name, func(t *testing.T) {
			m := New(nil, false)
			m.width = 80
			m.height = 24
			m.relayout()

			next, _ := m.Update(f.msg)
			got := next.(Model)

			if got.input.Value() != "\n" {
				t.Fatalf("shift+enter (%s) value = %q, want a single newline", f.name, got.input.Value())
			}
			if h := got.input.Height(); h != 2 {
				t.Fatalf("height after shift+enter (%s) = %d, want 2", f.name, h)
			}
		})
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
	m.mainChat().vp.SetContent(strings.Repeat("chat\n", 80))
	m.mainChat().SetYOffset(0)

	next, _ := m.Update(tea.MouseWheelMsg{X: 2, Y: m.scrollbarTop, Button: tea.MouseWheelDown})
	got := next.(Model)
	if got.mainChat().YOffset() == 0 {
		t.Fatal("wheel outside prompt should scroll chat viewport")
	}
}

func TestView_PinsPromptChromeToBottomWithShortOverlay(t *testing.T) {
	m := New(nil, false)
	m.width = 80
	m.height = 24
	m.splashShown = false
	m.relayout()
	dashboard, _ := newRuntimeDashboard(nil, m.palette, m.styles, m.width, m.height, dashboardModeModels)
	m.content = dashboard

	view := m.View()
	lines := strings.Split(view.Content, "\n")
	if len(lines) != m.height {
		t.Fatalf("view lines = %d, want %d:\n%s", len(lines), m.height, ansi.Strip(view.Content))
	}
	last := ansi.Strip(lines[len(lines)-1])
	if !strings.Contains(last, "/help for cmds") {
		t.Fatalf("status bar not pinned to last line: %q\n%s", last, ansi.Strip(view.Content))
	}
}

func TestContentPage_CtrlCUsesGlobalDoublePressQuit(t *testing.T) {
	m := New(nil, false)
	m.width = 80
	m.height = 24
	dashboard, _ := newRuntimeDashboard(nil, m.palette, m.styles, m.width, m.height, dashboardModeModels)
	m.content = dashboard

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("first ctrl+c should arm quit, not quit")
	}
	if !m.ctrlCArmed {
		t.Fatal("first ctrl+c should arm quit")
	}
	if m.input.Placeholder != armedInputPlaceholder {
		t.Fatalf("placeholder = %q, want %q", m.input.Placeholder, armedInputPlaceholder)
	}
	if m.content == nil {
		t.Fatal("first ctrl+c should keep the content page open")
	}

	_, cmd = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("second ctrl+c should return quit command")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("expected QuitMsg from second ctrl+c, got %T", msg)
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

func TestPrompt_ModifiedArrowNavigationVariants(t *testing.T) {
	for _, mod := range []tea.KeyMod{tea.ModSuper, tea.ModMeta} {
		p := newTestPromptInput(80)
		p.SetValue("hello brave world\nnext line")
		p, _ = updatePromptKey(p, tea.KeyLeft, "", mod)
		if got, want := p.cursor, len([]rune("hello brave world\n")); got != want {
			t.Fatalf("%v-left cursor = %d, want %d", mod, got, want)
		}
		p, _ = updatePromptKey(p, tea.KeyRight, "", mod)
		if got, want := p.cursor, len([]rune(p.Value())); got != want {
			t.Fatalf("%v-right cursor = %d, want %d", mod, got, want)
		}
		p, _ = updatePromptKey(p, tea.KeyUp, "", mod)
		if got := p.cursor; got != 0 {
			t.Fatalf("%v-up cursor = %d, want document start", mod, got)
		}
		p, _ = updatePromptKey(p, tea.KeyDown, "", mod)
		if got, want := p.cursor, len([]rune(p.Value())); got != want {
			t.Fatalf("%v-down cursor = %d, want document end %d", mod, got, want)
		}
	}
}

func TestPrompt_OptionMetaWordNavigationFallbacks(t *testing.T) {
	p := newTestPromptInput(80)
	p.SetValue("hello brave world")

	p, _ = updatePromptRune(p, 'b', tea.ModAlt)
	if got, want := p.cursor, len([]rune("hello brave ")); got != want {
		t.Fatalf("alt+b cursor = %d, want %d", got, want)
	}
	p, _ = updatePromptRune(p, 'f', tea.ModAlt)
	if got, want := p.cursor, len([]rune("hello brave world")); got != want {
		t.Fatalf("alt+f cursor = %d, want %d", got, want)
	}

	p, _ = updatePromptRune(p, 'b', tea.ModAlt|tea.ModShift)
	if got := p.selectedText(); got != "world" {
		t.Fatalf("shift+alt+b selected %q, want world", got)
	}
}

func TestPrompt_CtrlLineNavigationFallbacks(t *testing.T) {
	p := newTestPromptInput(80)
	p.SetValue("hello\nworld")

	p, _ = updatePromptRune(p, 'a', tea.ModCtrl)
	if got, want := p.cursor, len([]rune("hello\n")); got != want {
		t.Fatalf("ctrl+a cursor = %d, want line start %d", got, want)
	}
	p, _ = updatePromptRune(p, 'e', tea.ModCtrl)
	if got, want := p.cursor, len([]rune("hello\nworld")); got != want {
		t.Fatalf("ctrl+e cursor = %d, want line end %d", got, want)
	}
}

func TestPrompt_ShiftSelectionHorizontalDirections(t *testing.T) {
	p := newTestPromptInput(80)
	p.SetValue("abcde")
	p.CursorStart()

	p, _ = updatePromptKey(p, tea.KeyRight, "", tea.ModShift)
	if got := p.selectedText(); got != "a" {
		t.Fatalf("shift-right selected %q, want a", got)
	}
	p, _ = updatePromptKey(p, tea.KeyRight, "", tea.ModShift)
	if got := p.selectedText(); got != "ab" {
		t.Fatalf("second shift-right selected %q, want ab", got)
	}
	p, _ = updatePromptKey(p, tea.KeyLeft, "", tea.ModShift)
	if got := p.selectedText(); got != "a" {
		t.Fatalf("shift-left should shrink selection to %q, got %q", "a", got)
	}
	p, _ = updatePromptKey(p, tea.KeyLeft, "", tea.ModShift)
	if p.HasSelection() {
		t.Fatalf("shift-left back to anchor should clear selection, got %q", p.selectedText())
	}

	p.CursorEnd()
	p, _ = updatePromptKey(p, tea.KeyLeft, "", tea.ModShift)
	if got := p.selectedText(); got != "e" {
		t.Fatalf("shift-left from end selected %q, want e", got)
	}
	p, _ = updatePromptKey(p, tea.KeyLeft, "", tea.ModShift)
	if got := p.selectedText(); got != "de" {
		t.Fatalf("second shift-left selected %q, want de", got)
	}
	p, _ = updatePromptKey(p, tea.KeyRight, "", tea.ModShift)
	if got := p.selectedText(); got != "e" {
		t.Fatalf("shift-right should shrink selection to %q, got %q", "e", got)
	}
}

func TestPrompt_ShiftSelectionVisualRowDirections(t *testing.T) {
	p := newTestPromptInput(8) // 2 prompt cols + 6 text cols
	p.SetValue("abcdefghijkl")
	p.CursorStart()

	p, _ = updatePromptKey(p, tea.KeyDown, "", tea.ModShift)
	if got := p.selectedText(); got != "abcdef" {
		t.Fatalf("shift-down selected %q, want first visual row", got)
	}
	p, _ = updatePromptKey(p, tea.KeyUp, "", tea.ModShift)
	if p.HasSelection() {
		t.Fatalf("shift-up back to anchor should clear selection, got %q", p.selectedText())
	}

	p.CursorEnd()
	p, _ = updatePromptKey(p, tea.KeyUp, "", tea.ModShift)
	if got := p.selectedText(); got != "ghijkl" {
		t.Fatalf("shift-up from end selected %q, want second visual row", got)
	}
	p, _ = updatePromptKey(p, tea.KeyDown, "", tea.ModShift)
	if p.HasSelection() {
		t.Fatalf("shift-down back to anchor should clear selection, got %q", p.selectedText())
	}
}

func TestPrompt_ShiftDownExtendsAcrossMultipleWrappedRows(t *testing.T) {
	p := newTestPromptInput(8) // 2 prompt cols + 6 text cols
	p.SetValue("abcdefghijklmnopqr")
	p.CursorStart()

	p, _ = updatePromptKey(p, tea.KeyDown, "", tea.ModShift)
	if got := p.selectedText(); got != "abcdef" {
		t.Fatalf("first shift-down selected %q, want abcdef", got)
	}
	p, _ = updatePromptKey(p, tea.KeyDown, "", tea.ModShift)
	if got := p.selectedText(); got != "abcdefghijkl" {
		t.Fatalf("second shift-down selected %q, want abcdefghijkl", got)
	}
	p, _ = updatePromptKey(p, tea.KeyDown, "", tea.ModShift)
	if got := p.selectedText(); got != "abcdefghijklmnopqr" {
		t.Fatalf("third shift-down selected %q, want whole wrapped value", got)
	}
}

func TestPrompt_ShiftUpExtendsAcrossMultipleWrappedRows(t *testing.T) {
	p := newTestPromptInput(8) // 2 prompt cols + 6 text cols
	p.SetValue("abcdefghijklmnopqr")
	p.CursorEnd()

	p, _ = updatePromptKey(p, tea.KeyUp, "", tea.ModShift)
	if got := p.selectedText(); got != "mnopqr" {
		t.Fatalf("first shift-up selected %q, want mnopqr", got)
	}
	p, _ = updatePromptKey(p, tea.KeyUp, "", tea.ModShift)
	if got := p.selectedText(); got != "ghijklmnopqr" {
		t.Fatalf("second shift-up selected %q, want ghijklmnopqr", got)
	}
	p, _ = updatePromptKey(p, tea.KeyUp, "", tea.ModShift)
	if got := p.selectedText(); got != "abcdefghijklmnopqr" {
		t.Fatalf("third shift-up selected %q, want whole wrapped value", got)
	}
}

func TestPrompt_CursorAtSoftWrapBoundaryUsesNextVisualRow(t *testing.T) {
	p := newTestPromptInput(8) // 2 prompt cols + 6 text cols
	p.SetValue("abcdefghijkl")
	p.cursor = len([]rune("abcdef"))

	c := p.Cursor()
	if c == nil {
		t.Fatal("cursor unexpectedly nil")
	}
	if got, want := c.X, 2; got != want {
		t.Fatalf("cursor x at soft-wrap boundary = %d, want prompt column %d", got, want)
	}
	if got, want := c.Y, 1; got != want {
		t.Fatalf("cursor y at soft-wrap boundary = %d, want next visual row %d", got, want)
	}
}

func TestPrompt_RootShiftUpDownSelectsHardNewlineRows(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.input.SetValue("alpha\nbravo\ncharlie")
	m.relayout()

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift})
	got := next.(Model)
	if text := got.input.selectedText(); text != "\ncharlie" {
		t.Fatalf("root shift-up selected %q, want newline+charlie", text)
	}
	assertPromptViewHighlightsText(t, got.input.View(), "charlie")

	next, _ = got.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	got = next.(Model)
	if got.input.HasSelection() {
		t.Fatalf("root shift-down back to anchor should clear selection, got %q", got.input.selectedText())
	}

	m.input.CursorStart()
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	got = next.(Model)
	if text := got.input.selectedText(); text != "alpha\n" {
		t.Fatalf("root shift-down selected %q, want alpha+newline", text)
	}
	assertPromptViewHighlightsText(t, got.input.View(), "alpha")
}

func TestPrompt_ShiftUpFromHardLineStartSelectsPreviousLine(t *testing.T) {
	p := newStyledTestPromptInput(80)
	p.SetValue("alpha\nbravo\ncharlie")
	p.moveCursor(len([]rune("alpha\nbravo\n")), false)

	p, _ = updatePromptKey(p, tea.KeyUp, "", tea.ModShift)
	if got := p.selectedText(); got != "bravo\n" {
		t.Fatalf("shift-up from hard line start selected %q, want bravo+newline", got)
	}
	lines := strings.Split(p.View(), "\n")
	if len(lines) < 2 {
		t.Fatalf("prompt view has %d lines, want at least 2: %q", len(lines), p.View())
	}
	cells := ansiCells(lines[1])
	for _, r := range "bravo" {
		cell := mustCell(t, cells, r)
		if !cell.hasBackground {
			t.Fatalf("selected cell %q should have background style, cells=%+v", r, cells)
		}
	}
}

func TestPrompt_SelectedBlankLineBreakRendersHighlight(t *testing.T) {
	p := newStyledTestPromptInput(20)
	p.SetValue("alpha\n\ncharlie")
	p.moveCursor(len([]rune("alpha\n\n")), false)

	p, _ = updatePromptKey(p, tea.KeyUp, "", tea.ModShift)
	if got := p.selectedText(); got != "\n" {
		t.Fatalf("shift-up over blank line selected %q, want newline", got)
	}

	lines := strings.Split(p.View(), "\n")
	if len(lines) < 2 {
		t.Fatalf("prompt view has %d lines, want at least 2: %q", len(lines), p.View())
	}
	for _, cell := range ansiCells(lines[1]) {
		if cell.r == ' ' && cell.hasBackground {
			return
		}
	}
	t.Fatalf("blank selected line did not render highlighted padding: %q", lines[1])
}

func TestPrompt_SelectionRenderRestoresTextStyleAfterSelection(t *testing.T) {
	p := newStyledTestPromptInput(80)
	p.SetValue("abcd")
	p.selectionAnchor = 2
	p.cursor = 3

	cells := ansiCells(p.View())
	cCell := mustCell(t, cells, 'c')
	dCell := mustCell(t, cells, 'd')
	if cCell == nil || !cCell.hasForeground || !cCell.hasBackground {
		t.Fatalf("selected cell should have foreground and background style, cells=%+v", cells)
	}
	if dCell == nil || !dCell.hasForeground || dCell.hasBackground {
		t.Fatalf("text after selection should restore text foreground without selection background, cells=%+v", cells)
	}
}

func TestPrompt_ReverseSelectionRenderRestoresTextStyleAfterSelection(t *testing.T) {
	p := newStyledTestPromptInput(80)
	p.SetValue("abcdef")
	p.cursor = 4

	p, _ = updatePromptKey(p, tea.KeyLeft, "", tea.ModShift)
	p, _ = updatePromptKey(p, tea.KeyLeft, "", tea.ModShift)
	if got := p.selectedText(); got != "cd" {
		t.Fatalf("reverse selection selected %q, want cd", got)
	}

	cells := ansiCells(p.View())
	for _, r := range []rune{'c', 'd'} {
		cell := mustCell(t, cells, r)
		if !cell.hasForeground || !cell.hasBackground {
			t.Fatalf("selected cell %q should have foreground and background style, cells=%+v", r, cells)
		}
	}
	for _, r := range []rune{'b', 'e'} {
		cell := mustCell(t, cells, r)
		if !cell.hasForeground || cell.hasBackground {
			t.Fatalf("unselected cell %q should have text foreground and no selection background, cells=%+v", r, cells)
		}
	}
}

func TestPrompt_SelectionRenderAcrossWrappedRows(t *testing.T) {
	p := newStyledTestPromptInput(8) // 2 prompt cols + 6 text cols
	p.SetValue("abcdefghi")
	p.selectionAnchor = 2
	p.cursor = 8
	if got := p.selectedText(); got != "cdefgh" {
		t.Fatalf("wrapped selection selected %q, want cdefgh", got)
	}

	cells := ansiCells(p.View())
	for _, r := range []rune{'c', 'd', 'e', 'f', 'g', 'h'} {
		cell := mustCell(t, cells, r)
		if !cell.hasForeground || !cell.hasBackground {
			t.Fatalf("wrapped selected cell %q should have foreground and background style, cells=%+v", r, cells)
		}
	}
	for _, r := range []rune{'b', 'i'} {
		cell := mustCell(t, cells, r)
		if !cell.hasForeground || cell.hasBackground {
			t.Fatalf("wrapped unselected cell %q should have text foreground and no selection background, cells=%+v", r, cells)
		}
	}
}

func TestPrompt_ModifiedArrowsDoNotTriggerHistoryRecall(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.inputHistory = []string{"old prompt"}
	m.historyIdx = len(m.inputHistory)
	m.input.SetValue("draft")
	m.input.CursorStart()

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	got := next.(Model)
	if got.input.Value() != "draft" {
		t.Fatalf("shift-down recalled history instead of selecting in prompt: %q", got.input.Value())
	}
}

func TestPrompt_HomeEndRouteToPrompt(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.mainChat().vp.SetContent(strings.Repeat("chat\n", 80))
	m.mainChat().SetYOffset(10)
	m.input.SetValue("hello\nworld")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	got := next.(Model)
	if got.mainChat().YOffset() != 10 {
		t.Fatalf("home should not scroll viewport, yoffset=%d", got.mainChat().YOffset())
	}
	if want := len([]rune("hello\n")); got.input.cursor != want {
		t.Fatalf("home cursor = %d, want line start %d", got.input.cursor, want)
	}

	next, _ = got.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	got = next.(Model)
	if want := len([]rune("hello\nworld")); got.input.cursor != want {
		t.Fatalf("end cursor = %d, want line end %d", got.input.cursor, want)
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

// TestModel_PasteIntoEmptyRendersInView reproduces the reported bug at the
// Model level: the promptInput's own View() renders paste-into-empty
// correctly, so if the composed Model view is missing the text, the fault is
// in the composition path (m.Update handling, m.View assembly, or a stale
// bubble-tea-level render).
func TestModel_PasteIntoEmptyRendersInView(t *testing.T) {
	m := New(nil, false)
	m.width = 80
	m.height = 24
	m.relayout()

	pasted := "form fields - need radio and checkbox input components for "
	next, _ := m.Update(tea.PasteMsg{Content: pasted})
	got := next.(Model)

	if v := got.input.Value(); v != pasted {
		t.Fatalf("input buffer got %q, want %q", v, pasted)
	}
	view := stripAnsiCSI(got.View().Content)
	if !strings.Contains(view, pasted) {
		t.Errorf("Model.View() does not contain the pasted text:\n%s", view)
	}
	t.Logf("=== FULL VIEW after paste-into-empty ===\n%s\n=== END VIEW ===", view)
}

// TestPrompt_PasteIntoEmptyRendersInView is the direct reproducer for the
// reported paste bug: paste 60 characters into a fresh empty prompt; the
// buffer takes the content but the rendered view is missing it. If View()
// contains the pasted text, the bug lives above the promptInput layer (in
// the Model composition, cursor placement, or Bubble Tea plumbing).
func TestPrompt_PasteIntoEmptyRendersInView(t *testing.T) {
	p := newTestPromptInput(80)
	pasted := "form fields - need radio and checkbox input components for "
	p, _ = p.Update(tea.PasteMsg{Content: pasted})

	if got := p.Value(); got != pasted {
		t.Fatalf("buffer got %q, want %q", got, pasted)
	}
	if got, want := p.cursor, utf8.RuneCountInString(pasted); got != want {
		t.Errorf("cursor at %d, want %d (end of pasted text)", got, want)
	}
	view := stripAnsiCSI(p.View())
	if !strings.Contains(view, pasted) {
		t.Errorf("View() does not contain the pasted text — bug is in promptInput.\nview:\n%s", view)
	}
}

// TestPrompt_PasteIntoNonEmptyRendersInView is the control: paste after some
// existing content must also render. If the empty case fails but this one
// passes, the bug is specifically about the empty→non-empty transition.
func TestPrompt_PasteIntoNonEmptyRendersInView(t *testing.T) {
	p := newTestPromptInput(80)
	p, _ = updatePromptText(p, "existing ")
	pasted := "form fields - need radio and checkbox input components for "
	p, _ = p.Update(tea.PasteMsg{Content: pasted})

	view := stripAnsiCSI(p.View())
	if !strings.Contains(view, "existing "+pasted) {
		t.Errorf("View() missing existing+pasted text:\n%s", view)
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

func TestPrompt_UndoRedoKeyVariants(t *testing.T) {
	for _, tc := range []struct {
		name    string
		undoMod tea.KeyMod
		redoMod tea.KeyMod
	}{
		{name: "super", undoMod: tea.ModSuper, redoMod: tea.ModSuper | tea.ModShift},
		{name: "meta", undoMod: tea.ModMeta, redoMod: tea.ModMeta | tea.ModShift},
		{name: "ctrl", undoMod: tea.ModCtrl, redoMod: tea.ModCtrl | tea.ModShift},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestPromptInput(80)
			p, _ = p.Update(tea.PasteMsg{Content: "paste"})
			p, _ = updatePromptRune(p, 'z', tc.undoMod)
			if got := p.Value(); got != "" {
				t.Fatalf("%s undo = %q, want empty", tc.name, got)
			}
			p, _ = updatePromptRune(p, 'z', tc.redoMod)
			if got := p.Value(); got != "paste" {
				t.Fatalf("%s redo = %q, want paste", tc.name, got)
			}
		})
	}
}

func TestPrompt_CtrlUnderscoreUndoes(t *testing.T) {
	p := newTestPromptInput(80)
	p, _ = p.Update(tea.PasteMsg{Content: "paste"})
	p, _ = updatePromptRune(p, '_', tea.ModCtrl)
	if got := p.Value(); got != "" {
		t.Fatalf("ctrl+_ undo = %q, want empty", got)
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

func newStyledTestPromptInput(width int) promptInput {
	p := newTestPromptInput(width)
	p.SetStyles(promptInputStyles{
		Text:      lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		Selection: lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Background(lipgloss.Color("4")),
	})
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

type styledCell struct {
	r             rune
	hasForeground bool
	hasBackground bool
}

func ansiCells(s string) []styledCell {
	var cells []styledCell
	hasForeground := false
	hasBackground := false
	for i := 0; i < len(s); {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			if end := strings.IndexByte(s[i+2:], 'm'); end >= 0 {
				applySGR(s[i+2:i+2+end], &hasForeground, &hasBackground)
				i += end + 3
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r != '\n' {
			cells = append(cells, styledCell{
				r:             r,
				hasForeground: hasForeground,
				hasBackground: hasBackground,
			})
		}
		i += size
	}
	return cells
}

func applySGR(seq string, hasForeground, hasBackground *bool) {
	if seq == "" {
		*hasForeground = false
		*hasBackground = false
		return
	}
	for _, part := range strings.Split(seq, ";") {
		n, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		switch {
		case n == 0:
			*hasForeground = false
			*hasBackground = false
		case n == 39:
			*hasForeground = false
		case n == 49:
			*hasBackground = false
		case n == 38 || n >= 30 && n <= 37 || n >= 90 && n <= 97:
			*hasForeground = true
		case n == 48 || n >= 40 && n <= 47 || n >= 100 && n <= 107:
			*hasBackground = true
		}
	}
}

func findCell(cells []styledCell, r rune) *styledCell {
	for i := range cells {
		if cells[i].r == r {
			return &cells[i]
		}
	}
	return nil
}

func mustCell(t *testing.T, cells []styledCell, r rune) *styledCell {
	t.Helper()
	cell := findCell(cells, r)
	if cell == nil {
		t.Fatalf("cell %q not found in %+v", r, cells)
	}
	return cell
}

func assertPromptViewHighlightsText(t *testing.T, view, selected string) {
	t.Helper()
	idx := strings.Index(view, selected)
	if idx < 0 {
		t.Fatalf("selected text %q not found in view %q", selected, view)
	}
	prefix := view[:idx]
	esc := strings.LastIndex(prefix, "\x1b[")
	if esc < 0 || !strings.Contains(prefix[esc:], "48;") {
		t.Fatalf("selected text %q not preceded by background style in view %q", selected, view)
	}
}
