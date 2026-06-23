package ui

import (
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type selectionPoint struct {
	Line int
	Col  int
}

type textSelection struct {
	Active   bool
	Dragging bool
	Anchor   selectionPoint
	Cursor   selectionPoint
}

func plainLines(content string) []string {
	return strings.Split(ansi.Strip(content), "\n")
}

func (s textSelection) empty() bool {
	return s.Anchor == s.Cursor
}

func (s textSelection) hasRange() bool {
	return s.Active && !s.empty()
}

func (s textSelection) ordered() (selectionPoint, selectionPoint) {
	a, b := s.Anchor, s.Cursor
	if beforePoint(b, a) {
		return b, a
	}
	return a, b
}

func beforePoint(a, b selectionPoint) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Col < b.Col
}

func (s textSelection) lineRange(line, width int) (int, int, bool) {
	if !s.hasRange() {
		return 0, 0, false
	}
	start, end := s.ordered()
	if line < start.Line || line > end.Line {
		return 0, 0, false
	}
	switch {
	case start.Line == end.Line:
		return clampInt(start.Col, 0, width), clampInt(end.Col, 0, width), start.Col != end.Col
	case line == start.Line:
		return clampInt(start.Col, 0, width), width, start.Col < width
	case line == end.Line:
		return 0, clampInt(end.Col, 0, width), end.Col > 0
	default:
		return 0, width, width > 0
	}
}

func (m Model) mouseInViewportText(mouse tea.Mouse) bool {
	return mouse.X >= 0 &&
		mouse.X < m.viewport.Width() &&
		mouse.Y >= m.scrollbarTop &&
		mouse.Y < m.scrollbarTop+m.viewport.Height()
}

func (m *Model) beginSelection(mouse tea.Mouse) {
	m.selectionNotice = ""
	pt := m.selectionPointFromMouse(mouse, false)
	m.selection = textSelection{
		Active:   true,
		Dragging: true,
		Anchor:   pt,
		Cursor:   pt,
	}
}

func (m *Model) updateSelection(mouse tea.Mouse, allowScroll bool) {
	if !m.selection.Active {
		return
	}
	m.selection.Cursor = m.selectionPointFromMouse(mouse, allowScroll)
}

func (m *Model) clearSelection() {
	m.selection = textSelection{}
}

func (m *Model) selectionPointFromMouse(mouse tea.Mouse, allowScroll bool) selectionPoint {
	height := m.viewport.Height()
	row := mouse.Y - m.scrollbarTop
	if allowScroll {
		switch {
		case row < 0:
			m.viewport.ScrollUp(1)
			row = 0
		case row >= height:
			m.viewport.ScrollDown(1)
			row = height - 1
		}
	}
	row = clampInt(row, 0, maxInt(0, height-1))
	line := m.viewport.YOffset() + row
	if len(m.viewportPlainLines) > 0 {
		line = clampInt(line, 0, len(m.viewportPlainLines)-1)
	}
	return selectionPoint{
		Line: line,
		Col:  clampInt(mouse.X, 0, m.viewport.Width()),
	}
}

// selectionBg is the SGR for the selection background — a muted slate blue laid
// UNDER the existing text so the syntax colors show through, like a native
// selection rather than a flat one-color block.
const selectionBg = "\x1b[48;2;45;79;97m" // #2D4F61

var ansiResetRe = regexp.MustCompile("\x1b\\[0?m")

func (m Model) renderSelectionOnLine(line string, contentLine int) string {
	start, end, ok := m.selection.lineRange(contentLine, m.viewport.Width())
	if !ok {
		return line
	}
	return highlightRange(line, start, end)
}

// highlightRange overlays the selection background on the visible columns
// [start,end) of an already-styled line, preserving the per-character foreground
// colors. The background is re-applied after every SGR reset so inner resets
// don't drop it.
func highlightRange(line string, start, end int) string {
	w := ansi.StringWidth(line)
	if start < 0 {
		start = 0
	}
	if end > w {
		end = w
	}
	if start >= end {
		return line
	}
	before := ansi.Cut(line, 0, start)
	mid := ansi.Cut(line, start, end)
	after := ansi.Cut(line, end, w)
	mid = selectionBg + ansiResetRe.ReplaceAllStringFunc(mid, func(r string) string {
		return r + selectionBg
	}) + "\x1b[0m"
	return before + mid + after
}

func (m Model) handleSelectionKey(msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		m.clearSelection()
		return m, nil, true
	case "enter", "c", "y", "ctrl+c":
		text := m.selectedText()
		if text == "" {
			m.clearSelection()
			return m, nil, true
		}
		m.clearSelection()
		m.selectionNotice = "copied selection"
		return m, selectionClipboardCmd(text), true
	}
	if isSelectionCopyKey(msg) {
		text := m.selectedText()
		if text == "" {
			m.clearSelection()
			return m, nil, true
		}
		m.clearSelection()
		m.selectionNotice = "copied selection"
		return m, selectionClipboardCmd(text), true
	}

	// Let navigation keys keep the selection while the viewport scrolls. If the
	// user starts typing, clear the highlight and pass the key through to the
	// input so the app still feels like a normal prompt.
	if msg.Text != "" {
		m.clearSelection()
	}
	return m, nil, false
}

func isSelectionCopyKey(msg tea.KeyPressMsg) bool {
	key := msg.Key()
	if key.Code != 'c' && key.Code != 'C' && key.BaseCode != 'c' && key.BaseCode != 'C' {
		return false
	}
	return key.Mod.Contains(tea.ModSuper) || key.Mod.Contains(tea.ModMeta)
}

func selectionClipboardCmd(text string) tea.Cmd {
	return tea.Batch(tea.SetClipboard(text), pbcopyCmd(text))
}

func pbcopyCmd(text string) tea.Cmd {
	return func() tea.Msg {
		if runtime.GOOS != "darwin" {
			return nil
		}
		cmd := exec.Command("/usr/bin/pbcopy")
		cmd.Stdin = strings.NewReader(text)
		_ = cmd.Run()
		return nil
	}
}

func (m Model) selectedText() string {
	if !m.selection.hasRange() || len(m.viewportPlainLines) == 0 {
		return ""
	}
	start, end := m.selection.ordered()
	start.Line = clampInt(start.Line, 0, len(m.viewportPlainLines)-1)
	end.Line = clampInt(end.Line, 0, len(m.viewportPlainLines)-1)
	if beforePoint(end, start) {
		return ""
	}

	parts := make([]string, 0, end.Line-start.Line+1)
	for line := start.Line; line <= end.Line; line++ {
		text := m.viewportPlainLines[line]
		switch {
		case start.Line == end.Line:
			parts = append(parts, ansi.Cut(text, start.Col, end.Col))
		case line == start.Line:
			parts = append(parts, ansi.Cut(text, start.Col, ansi.StringWidth(text)))
		case line == end.Line:
			parts = append(parts, ansi.Cut(text, 0, end.Col))
		default:
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
