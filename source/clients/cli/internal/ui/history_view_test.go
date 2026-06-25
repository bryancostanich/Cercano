package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
)

// keyPress builds a KeyPressMsg for special-key names ("enter", "up", "down",
// "esc", "q", etc.) exactly as existing ui tests do: set Code to the tea
// constant so msg.String() returns the expected name.
func keyPress(s string) tea.KeyPressMsg {
	var code rune
	switch s {
	case "enter":
		code = tea.KeyEnter
	case "up":
		code = tea.KeyUp
	case "down":
		code = tea.KeyDown
	case "esc":
		code = tea.KeyEscape
	default:
		// Printable single-rune keys (e.g. "q")
		if len(s) == 1 {
			code = rune(s[0])
		}
	}
	return tea.KeyPressMsg{Code: code}
}

// newTestHistoryView builds a historyView from hand-made rows, bypassing the
// agent (newHistoryView needs a live client; the pure render/nav logic does not).
func newTestHistoryView(rows []histRow, w, h int) *historyView {
	s := theme.NewStyles(theme.Cracker())
	return &historyView{
		palette: theme.Cracker(), styles: s,
		width: w, height: h, rows: rows, cursor: 0,
		md: newHistoryMarkdown(),
	}
}

func TestHistoryUpdate_EnterResumesSelected(t *testing.T) {
	rows := []histRow{{id: "a", name: "first"}, {id: "b", name: "second"}}
	h := newTestHistoryView(rows, 100, 30)
	h.cursor = 1
	cmd, closed := h.Update(keyPress("enter"))
	if !closed {
		t.Fatalf("enter should close the page")
	}
	if cmd == nil {
		t.Fatalf("enter should return a resume command")
	}
	msg := cmd()
	rr, ok := msg.(resumeRequestedMsg)
	if !ok {
		t.Fatalf("cmd msg = %T, want resumeRequestedMsg", msg)
	}
	if rr.ConversationID != "b" {
		t.Errorf("resumed %q, want b", rr.ConversationID)
	}
}

func TestHistoryUpdate_DownMovesCursorClamped(t *testing.T) {
	rows := []histRow{{id: "a"}, {id: "b"}}
	h := newTestHistoryView(rows, 100, 30)
	h.Update(keyPress("down"))
	if h.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", h.cursor)
	}
	h.Update(keyPress("down")) // clamp at last
	if h.cursor != 1 {
		t.Fatalf("cursor = %d, want clamped at 1", h.cursor)
	}
}

func TestHistoryUpdate_UpMovesCursorClamped(t *testing.T) {
	rows := []histRow{{id: "a"}, {id: "b"}}
	h := newTestHistoryView(rows, 100, 30)
	h.cursor = 1
	h.Update(keyPress("up"))
	if h.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", h.cursor)
	}
	h.Update(keyPress("up")) // clamp at first
	if h.cursor != 0 {
		t.Fatalf("cursor = %d, want clamped at 0", h.cursor)
	}
}

func TestHistoryUpdate_EscCloses(t *testing.T) {
	rows := []histRow{{id: "a"}}
	h := newTestHistoryView(rows, 100, 30)
	_, closed := h.Update(keyPress("esc"))
	if !closed {
		t.Fatalf("esc should close the page")
	}
}

func TestHistoryUpdate_ScrollToCursor(t *testing.T) {
	// 20 rows * 2 lines each + heading lines; viewport height 10
	rows := make([]histRow, 20)
	for i := range rows {
		rows[i] = histRow{id: "x", name: "n", recap: "r", meta: "m"}
	}
	h := newTestHistoryView(rows, 100, 10)
	// Move to last row; scrollToCursor must bring it into view.
	for i := 0; i < 19; i++ {
		h.Update(keyPress("down"))
	}
	height := dashboardContentHeight(10)
	_, meta := h.rowsLines()
	firstLine := -1
	for i, m := range meta {
		if m.row == h.cursor {
			firstLine = i
			break
		}
	}
	if firstLine < h.scrollOffset || firstLine >= h.scrollOffset+height {
		t.Errorf("cursor row first line %d not within viewport [%d, %d)", firstLine, h.scrollOffset, h.scrollOffset+height)
	}
}

func TestHistoryRowsLines_HeadingAndTwoLineRows(t *testing.T) {
	rows := []histRow{
		{id: "a", name: "read the cercano readme", recap: "Familiarized with the CLI", meta: "14 turns · 1h ago · opus-4-7"},
	}
	h := newTestHistoryView(rows, 100, 30)
	lines, meta := h.rowsLines()
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "History") {
		t.Fatalf("expected a History heading, got:\n%s", joined)
	}
	// Find the row's two lines via meta (row index 0, non-heading).
	var rowLines []string
	for i, m := range meta {
		if m.row == 0 {
			rowLines = append(rowLines, lines[i])
		}
	}
	if len(rowLines) != 2 {
		t.Fatalf("collapsed row should be 2 lines, got %d", len(rowLines))
	}
	if !strings.Contains(rowLines[0], "read the cercano readme") || !strings.Contains(rowLines[0], "14 turns") {
		t.Errorf("line 1 should carry name + meta: %q", rowLines[0])
	}
	if !strings.Contains(rowLines[1], "Familiarized") {
		t.Errorf("line 2 should carry the recap: %q", rowLines[1])
	}
}

func TestHistoryRowsLines_LongContentDoesNotExceedWidth(t *testing.T) {
	rows := []histRow{
		{id: "a", name: strings.Repeat("long name ", 20), recap: strings.Repeat("long recap ", 20), meta: "2 turns · 1d ago · m"},
	}
	h := newTestHistoryView(rows, 80, 30)
	lines, _ := h.rowsLines()
	for _, ln := range lines {
		if w := lipgloss.Width(ln); w > dashboardPanelWidth(80) {
			t.Fatalf("line wider than panel (%d > %d): %q", w, dashboardPanelWidth(80), ln)
		}
	}
}

func TestHistoryScrollState_BoundsToContentHeight(t *testing.T) {
	rows := make([]histRow, 50)
	for i := range rows {
		rows[i] = histRow{id: "x", name: "n", recap: "r", meta: "m"}
	}
	h := newTestHistoryView(rows, 100, 24)
	st := h.ScrollState()
	if st.Height != dashboardContentHeight(24) {
		t.Errorf("Height = %d, want %d", st.Height, dashboardContentHeight(24))
	}
	if st.Total <= st.Height {
		t.Errorf("Total (%d) should exceed Height (%d) for 50 rows", st.Total, st.Height)
	}
}
