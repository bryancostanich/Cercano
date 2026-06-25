package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// histRow is one conversation in the history list.
type histRow struct {
	id, name, recap, meta string
	expanded, turnsLoaded bool
	turns                 []agentclient.ContextTurn
}

// histLineMeta parallels each rendered line for hit-testing. row == -1 marks a
// heading/spacer line (not selectable); arrowCell marks the row's first line
// (where the ▸/▾ arrow sits and a click toggles expand).
type histLineMeta struct {
	row       int
	arrowCell bool
}

type historyView struct {
	width, height int
	palette       theme.Palette
	styles        theme.Styles
	agent         *agentclient.Client
	currentID     string
	rows          []histRow
	cursor        int
	scrollOffset  int
	md            *render.Markdown
}

func newHistoryMarkdown() *render.Markdown { return render.NewMarkdown(theme.CrackerMarkdownStyle()) }

// newHistoryView loads the conversation list synchronously (matching the old
// picker + contextView) and returns the page. The turn drawer loads lazily.
func newHistoryView(ag *agentclient.Client, p theme.Palette, s theme.Styles, currentID string, w, h int) (*historyView, tea.Cmd) {
	hv := &historyView{
		palette: p, styles: s, agent: ag, currentID: currentID,
		width: w, height: h, cursor: 0, md: newHistoryMarkdown(),
	}
	hv.rows = loadHistoryRows(ag)
	return hv, nil
}

// loadHistoryRows snapshots conversations into histRows (newest first as the
// agent returns them).
func loadHistoryRows(ag *agentclient.Client) []histRow {
	if ag == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	convs, err := ag.ListConversations(ctx, "", 100)
	if err != nil {
		return nil
	}
	rows := make([]histRow, 0, len(convs))
	for _, c := range convs {
		name := c.Title
		if name == "" {
			name = "(untitled)"
		}
		meta := fmt.Sprintf("%d turn", c.TurnCount)
		if c.TurnCount != 1 {
			meta += "s"
		}
		meta += " · " + relativeTime(c.LastTurnAt)
		if c.Model != "" {
			meta += " · " + c.Model
		}
		rows = append(rows, histRow{id: c.ID, name: name, recap: c.Recap, meta: meta})
	}
	return rows
}

func (h *historyView) ID() contentPageID { return contentPageHistory }

func (h *historyView) SetSize(w, hgt int) {
	h.width = w
	h.height = hgt
	h.clampScroll()
}

func (h *historyView) Update(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc", "q":
		return nil, true
	case "up", "k":
		h.moveCursor(-1)
	case "down", "j":
		h.moveCursor(1)
	case "enter":
		if h.cursor < 0 || h.cursor >= len(h.rows) {
			return nil, false
		}
		r := h.rows[h.cursor]
		return func() tea.Msg { return resumeRequestedMsg{ConversationID: r.id, Title: r.name} }, true
	case "pgup", "ctrl+b":
		h.ScrollBy(-dashboardContentHeight(h.height))
	case "pgdown", "ctrl+f":
		h.ScrollBy(dashboardContentHeight(h.height))
	}
	return nil, false
}

// moveCursor shifts the selection by dir (clamped) and scrolls so the selected
// row's first line stays within the viewport window.
func (h *historyView) moveCursor(dir int) {
	if len(h.rows) == 0 {
		return
	}
	h.cursor = clampInt(h.cursor+dir, 0, len(h.rows)-1)
	h.scrollToCursor()
}

// scrollToCursor adjusts scrollOffset so the selected row's first line is within
// [scrollOffset, scrollOffset+height).
func (h *historyView) scrollToCursor() {
	_, meta := h.rowsLines()
	first := -1
	for i, m := range meta {
		if m.row == h.cursor {
			first = i
			break
		}
	}
	if first < 0 {
		return
	}
	height := dashboardContentHeight(h.height)
	if first < h.scrollOffset {
		h.scrollOffset = first
	} else if first >= h.scrollOffset+height {
		h.scrollOffset = first - height + 1
	}
	h.clampScroll()
}

// rowsLines renders the # History heading then two lines per row, returning
// parallel line + meta slices. Indent and truncation keep every line within the
// panel width.
func (h *historyView) rowsLines() ([]string, []histLineMeta) {
	var lines []string
	var meta []histLineMeta
	add := func(s string, m histLineMeta) { lines = append(lines, s); meta = append(meta, m) }

	panelW := dashboardPanelWidth(h.width)
	for _, hl := range strings.Split(h.md.Render("# History", panelW), "\n") {
		add(hl, histLineMeta{row: -1})
	}
	add("", histLineMeta{row: -1})

	if len(h.rows) == 0 {
		add(h.styles.Muted.Render("  (no saved conversations)"), histLineMeta{row: -1})
		return lines, meta
	}

	for i := range h.rows {
		h.appendRow(&lines, &meta, i, panelW)
	}
	return lines, meta
}

// appendRow renders one collapsed row: line 1 = arrow + name + right-aligned
// meta; line 2 = indented recap preview. (Expanded drawer: Task 5.)
func (h *historyView) appendRow(lines *[]string, meta *[]histLineMeta, i, panelW int) {
	add := func(s string, m histLineMeta) { *lines = append(*lines, s); *meta = append(*meta, m) }
	r := h.rows[i]

	arrow := "▸ "
	nameStyle := h.styles.Muted
	if i == h.cursor {
		arrow = h.styles.Accent.Render("▸ ")
		nameStyle = h.styles.Bright
	} else {
		arrow = h.styles.Dim.Render(arrow)
	}

	// Line 1: " <arrow><name padded>  <meta>" budgeted so meta sits at the right.
	metaCell := h.styles.Muted.Render(r.meta)
	metaW := lipgloss.Width(metaCell)
	const lead = 1 + 2 // leading space + arrow cell
	nameW := panelW - lead - metaW - 2 // 2-space gap before meta
	if nameW < 8 {
		nameW = 8
	}
	nameTxt := ansi.Truncate(r.name, nameW, "…")
	nameCell := nameStyle.Render(nameTxt) + strings.Repeat(" ", maxInt(0, nameW-lipgloss.Width(nameTxt)))
	line1 := " " + arrow + nameCell + "  " + metaCell
	add(line1, histLineMeta{row: i, arrowCell: true})

	// Line 2: indented recap preview.
	recap := r.recap
	if strings.TrimSpace(recap) == "" {
		recap = "(no recap)"
	}
	indent := "      "
	recapTxt := ansi.Truncate(recap, maxInt(8, panelW-lipgloss.Width(indent)), "…")
	add(indent+h.styles.Primary.Render(recapTxt), histLineMeta{row: i})
}

func (h *historyView) View() string {
	lines, _ := h.rowsLines()
	height := dashboardContentHeight(h.height)
	h.scrollOffset = clampInt(h.scrollOffset, 0, maxInt(0, len(lines)-height))
	return renderScrollable(lines, height, dashboardPanelWidth(h.width), h.scrollOffset, h.styles)
}

// --- scroller ---

func (h *historyView) ScrollBy(delta int)  { h.scrollOffset += delta; h.clampScroll() }
func (h *historyView) ScrollTo(offset int) { h.scrollOffset = offset; h.clampScroll() }
func (h *historyView) ScrollState() contentPageScrollState {
	total := h.lineCount()
	height := dashboardContentHeight(h.height)
	return contentPageScrollState{Total: total, Height: height, Offset: clampInt(h.scrollOffset, 0, maxInt(0, total-height))}
}
func (h *historyView) clampScroll() { h.scrollOffset = h.ScrollState().Offset }
func (h *historyView) lineCount() int { l, _ := h.rowsLines(); return len(l) }
