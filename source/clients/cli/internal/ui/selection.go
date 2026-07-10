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

// selectionBg is the SGR for the selection background — a mid slate blue laid
// UNDER the existing text so the syntax colors show through, like a native
// selection rather than a flat one-color block. It is deliberately lighter than
// the sent-prompt navy fill (#1F4163): the older, darker slate (#2D4F61) sat
// within ~1.2:1 luminance of that navy, so a selection on a prompt line was
// nearly indistinguishable. This value lifts selection-vs-navy contrast to
// ~2.6:1 while staying dark enough to keep light foreground text readable.
const selectionBg = "\x1b[48;2;88;130;158m" // #58829E

// ansiSGRRe matches any SGR escape sequence. highlightRange re-asserts the
// selection background after *every* SGR (not just resets) so a background set
// by the line itself — e.g. the navy fill behind sent-prompt lines — can't
// override the selection and hide it. Matching only resets let such a
// background-set SGR (which has no following reset) win, leaving the selection
// invisible on any line with its own background.
var ansiSGRRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// highlightRange overlays the selection background on the visible columns
// [start,end) of an already-styled line, preserving the per-character foreground
// colors. The selection background is re-asserted after every SGR so inner
// style changes — including background fills — don't drop or override it.
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
	mid = selectionBg + ansiSGRRe.ReplaceAllStringFunc(mid, func(r string) string {
		return r + selectionBg
	}) + "\x1b[0m"
	return before + mid + after
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
