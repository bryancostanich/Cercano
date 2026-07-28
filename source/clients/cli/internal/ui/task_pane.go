package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	// Keep the pane off narrow/standard 80-column terminals so the existing
	// right-edge scrollbar remains clickable. The side drawer only appears when
	// there is enough horizontal room to reserve a tab without stealing core UI.
	taskPaneMinTerminalWidth = 100
	taskPaneCollapsedWidth   = 1
	taskPaneDefaultWidth     = 36
	taskPaneMinWidth         = 24
)

type taskPaneState struct {
	Expanded bool
	Width    int // expanded width including the left border; zero -> default
}

func (m Model) taskPaneAvailable() bool {
	return !m.contentPageActive() && m.width >= taskPaneMinTerminalWidth
}

func (m Model) taskPaneWidth() int {
	if !m.taskPaneAvailable() {
		return 0
	}
	if !m.taskPane.Expanded {
		return taskPaneCollapsedWidth
	}
	w := m.taskPane.Width
	if w <= 0 {
		w = taskPaneDefaultWidth
	}
	if w < taskPaneMinWidth {
		w = taskPaneMinWidth
	}
	max := m.width / 2
	if max < taskPaneMinWidth {
		max = taskPaneMinWidth
	}
	if w > max {
		w = max
	}
	return w
}

func (m *Model) toggleTaskPane() {
	if !m.taskPaneAvailable() {
		return
	}
	m.taskPane.Expanded = !m.taskPane.Expanded
	m.relayout()
}

func (m Model) taskPaneHit(x, y int) bool {
	w := m.taskPaneWidth()
	if w == 0 {
		return false
	}
	return x >= m.width-w && y >= m.scrollbarTop && y < m.scrollbarTop+m.activeChat().Height()
}

func (m Model) renderViewportWithTaskPane() string {
	chat := m.activeChat().View()
	paneW := m.taskPaneWidth()
	if paneW == 0 {
		return chat
	}
	pane := m.renderTaskPane(paneW, m.activeChat().Height())
	return joinColumns(chat, pane)
}

func (m Model) renderTaskPane(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	border := m.styles.BorderDim.Render("│")
	if width == taskPaneCollapsedWidth {
		glyph := "◀"
		if m.taskPane.Expanded {
			glyph = "▶"
		}
		lines := make([]string, height)
		for i := range lines {
			if i == 0 {
				lines[i] = m.styles.Accent.Render(glyph)
			} else if i >= 2 && i < 7 {
				letters := []rune("TASKS")
				lines[i] = m.styles.Muted.Render(string(letters[i-2]))
			} else {
				lines[i] = border
			}
		}
		return strings.Join(lines, "\n")
	}

	innerW := width - 2 // left border + right pad/edge
	if innerW < 1 {
		innerW = 1
	}
	header := "▶ Tasks"
	if !m.taskPane.Expanded {
		header = "◀ Tasks"
	}
	lines := []string{
		border + fitCell(m.styles.Accent.Render(header), innerW),
		border + m.styles.BorderDim.Render(strings.Repeat("─", innerW)),
		border + fitCell(m.styles.Muted.Render("No task tree loaded yet."), innerW),
		border + fitCell(m.styles.Muted.Render("Plan status will appear here."), innerW),
		border + fitCell(m.styles.Muted.Render("Toggle: ctrl+t or click tab."), innerW),
	}
	for len(lines) < height {
		lines = append(lines, border+strings.Repeat(" ", innerW))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func joinColumns(left, right string) string {
	ll := strings.Split(left, "\n")
	rl := strings.Split(right, "\n")
	n := len(ll)
	if len(rl) > n {
		n = len(rl)
	}
	leftW := maxDisplayWidth(ll)
	out := make([]string, n)
	for i := 0; i < n; i++ {
		l, r := "", ""
		if i < len(ll) {
			l = ll[i]
		}
		if i < len(rl) {
			r = rl[i]
		}
		out[i] = fitCell(l, leftW) + r
	}
	return strings.Join(out, "\n")
}

func maxDisplayWidth(lines []string) int {
	max := 0
	for _, l := range lines {
		if w := ansi.StringWidth(ansi.Strip(l)); w > max {
			max = w
		}
	}
	return max
}

func fitCell(s string, width int) string {
	plainW := ansi.StringWidth(ansi.Strip(s))
	if plainW > width {
		return lipgloss.NewStyle().MaxWidth(width).Render(s)
	}
	return s + strings.Repeat(" ", width-plainW)
}
