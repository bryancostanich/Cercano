package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/server/pkg/agentclient"
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
	Tasks    map[string]taskPaneTask
	Roots    []string
}

type taskPaneTask struct {
	ID       string
	Title    string
	Status   string
	ParentID string
	Children []string
}

func (m Model) taskPaneHasTasks() bool {
	return len(m.taskPane.Tasks) > 0
}

func (m Model) taskPaneAvailable() bool {
	return m.taskPaneHasTasks() && !m.contentPageActive() && m.width >= taskPaneMinTerminalWidth
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

func (m *Model) applyTaskChange(kind string, task *agentclient.TaskNode) {
	if task == nil || task.ID == "" {
		return
	}
	if m.taskPane.Tasks == nil {
		m.taskPane.Tasks = make(map[string]taskPaneTask)
	}
	if kind == "removed" {
		removeTaskPaneSubtree(m.taskPane.Tasks, task)
		m.taskPane.Roots = removeTaskPaneID(m.taskPane.Roots, task.ID)
		for id, existing := range m.taskPane.Tasks {
			existing.Children = removeTaskPaneID(existing.Children, task.ID)
			m.taskPane.Tasks[id] = existing
		}
		if len(m.taskPane.Tasks) == 0 && m.taskPane.Expanded {
			m.taskPane.Expanded = false
		}
		m.relayout()
		return
	}
	upsertTaskPaneSubtree(m.taskPane.Tasks, task)
	if task.ParentID == "" || m.taskPane.Tasks[task.ParentID].ID == "" {
		m.taskPane.Roots = appendTaskPaneID(m.taskPane.Roots, task.ID)
	} else {
		m.taskPane.Roots = removeTaskPaneID(m.taskPane.Roots, task.ID)
		parent := m.taskPane.Tasks[task.ParentID]
		parent.Children = appendTaskPaneID(parent.Children, task.ID)
		m.taskPane.Tasks[parent.ID] = parent
	}
	m.relayout()
}

func upsertTaskPaneSubtree(dst map[string]taskPaneTask, task *agentclient.TaskNode) {
	if task == nil || task.ID == "" {
		return
	}
	children := make([]string, 0, len(task.Children))
	for i := range task.Children {
		child := &task.Children[i]
		if child.ID == "" {
			continue
		}
		children = append(children, child.ID)
		upsertTaskPaneSubtree(dst, child)
	}
	dst[task.ID] = taskPaneTask{
		ID:       task.ID,
		Title:    task.Title,
		Status:   task.Status,
		ParentID: task.ParentID,
		Children: children,
	}
}

func removeTaskPaneSubtree(dst map[string]taskPaneTask, task *agentclient.TaskNode) {
	if task == nil || task.ID == "" {
		return
	}
	for i := range task.Children {
		removeTaskPaneSubtree(dst, &task.Children[i])
	}
	delete(dst, task.ID)
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
	}
	for _, line := range m.taskPaneLines(innerW) {
		lines = append(lines, border+line)
	}
	for len(lines) < height {
		lines = append(lines, border+strings.Repeat(" ", innerW))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func (m Model) taskPaneLines(width int) []string {
	if width <= 0 {
		return nil
	}
	if len(m.taskPane.Tasks) == 0 {
		return []string{fitCell(m.styles.Muted.Render("No tasks."), width)}
	}
	roots := m.taskPane.Roots
	if len(roots) == 0 {
		roots = deriveTaskPaneRoots(m.taskPane.Tasks)
	}
	lines := make([]string, 0, len(m.taskPane.Tasks))
	seen := make(map[string]bool, len(m.taskPane.Tasks))
	for _, id := range roots {
		m.appendTaskPaneTaskLines(&lines, seen, id, 0, width)
	}
	// Be forgiving if updates arrived out of order and left an orphaned task not
	// reachable from the root list. Render it rather than silently dropping it.
	for id := range m.taskPane.Tasks {
		if !seen[id] {
			m.appendTaskPaneTaskLines(&lines, seen, id, 0, width)
		}
	}
	if len(lines) == 0 {
		return []string{fitCell(m.styles.Muted.Render("No tasks."), width)}
	}
	return lines
}

func (m Model) appendTaskPaneTaskLines(lines *[]string, seen map[string]bool, id string, depth, width int) {
	if seen[id] {
		return
	}
	task, ok := m.taskPane.Tasks[id]
	if !ok {
		return
	}
	seen[id] = true
	indent := strings.Repeat("  ", depth)
	glyph := taskPaneStatusGlyph(task.Status)
	text := indent + glyph + " " + task.Title
	style := lipgloss.NewStyle()
	switch task.Status {
	case "done":
		style = m.styles.Muted.Strikethrough(true)
	case "in_progress":
		style = m.styles.Accent
	case "blocked":
		style = m.styles.Error
	default:
		style = m.styles.Primary
	}
	*lines = append(*lines, fitCell(style.Render(text), width))
	for _, childID := range task.Children {
		m.appendTaskPaneTaskLines(lines, seen, childID, depth+1, width)
	}
}

func taskPaneStatusGlyph(status string) string {
	switch status {
	case "done":
		return "✓"
	case "in_progress":
		return "~"
	case "blocked":
		return "!"
	default:
		return "☐"
	}
}

func deriveTaskPaneRoots(tasks map[string]taskPaneTask) []string {
	roots := make([]string, 0)
	for id, task := range tasks {
		if task.ParentID == "" {
			roots = append(roots, id)
			continue
		}
		if _, ok := tasks[task.ParentID]; !ok {
			roots = append(roots, id)
		}
	}
	return roots
}

func appendTaskPaneID(ids []string, id string) []string {
	if id == "" {
		return ids
	}
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func removeTaskPaneID(ids []string, id string) []string {
	out := ids[:0]
	for _, existing := range ids {
		if existing != id {
			out = append(out, existing)
		}
	}
	return out
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
