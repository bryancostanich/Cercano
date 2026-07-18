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

// resumeRequestedMsg is emitted when the user selects a conversation; the root
// model turns it into applyResume + an active-conversation switch.
type resumeRequestedMsg struct {
	ConversationID string
	Title          string
}

// relativeTime renders a coarse "5m ago" / "3h ago" / "2d ago" string.
func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < 60*time.Second:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

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
	styles        theme.Styles
	agent         *agentclient.Client
	rows          []histRow
	allRows       []histRow
	filter        string
	filtering     bool
	cursor        int
	scrollOffset  int
	md            *render.Markdown
}

func newHistoryMarkdown(p theme.Palette) *render.Markdown {
	return render.NewMarkdown(theme.MarkdownStyle(p))
}

// newHistoryView loads the conversation list synchronously (matching the old
// picker + contextView) and returns the page. The turn drawer loads lazily.
func newHistoryView(ag *agentclient.Client, p theme.Palette, s theme.Styles, w, h int) (*historyView, tea.Cmd) {
	hv := &historyView{
		styles: s, agent: ag,
		width: w, height: h, cursor: 0, md: newHistoryMarkdown(p),
	}
	hv.allRows = loadHistoryRows(ag)
	hv.applyFilter()
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
	if h.filtering {
		if handled := h.updateFilter(msg); handled {
			return nil, false
		}
	}
	switch msg.String() {
	case "esc", "q":
		return nil, true
	case "/":
		h.filtering = true
		return nil, false
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
	case "right", "l":
		return h.expandSelected(), false
	case "left", "h":
		if h.cursor >= 0 && h.cursor < len(h.rows) {
			h.rows[h.cursor].expanded = false
		}
	case "pgup", "ctrl+b":
		h.ScrollBy(-dashboardContentHeight(h.height))
	case "pgdown", "ctrl+f":
		h.ScrollBy(dashboardContentHeight(h.height))
	}
	return nil, false
}

func (h *historyView) updateFilter(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "enter":
		h.filtering = false
		return true
	case "esc":
		h.filtering = false
		return true
	case "backspace":
		if h.filter != "" {
			r := []rune(h.filter)
			h.filter = string(r[:len(r)-1])
			h.applyFilter()
		}
		return true
	case "ctrl+u":
		h.filter = ""
		h.applyFilter()
		return true
	}
	text := msg.Text
	if text == "" && msg.Code >= 32 && msg.Code != 127 && msg.Mod == 0 {
		text = string(msg.Code)
	}
	if text == "" || strings.Contains(text, "\n") {
		return false
	}
	h.filter += text
	h.applyFilter()
	return true
}

func (h *historyView) syncVisibleRowsToAll() {
	if len(h.rows) == 0 || len(h.allRows) == 0 {
		return
	}
	byID := make(map[string]histRow, len(h.rows))
	for _, r := range h.rows {
		byID[r.id] = r
	}
	for i := range h.allRows {
		if r, ok := byID[h.allRows[i].id]; ok {
			h.allRows[i] = r
		}
	}
}

func (h *historyView) applyFilter() {
	h.syncVisibleRowsToAll()
	q := strings.ToLower(strings.TrimSpace(h.filter))
	if len(h.allRows) == 0 && len(h.rows) > 0 {
		h.allRows = append([]histRow(nil), h.rows...)
	}
	if q == "" {
		h.rows = append([]histRow(nil), h.allRows...)
	} else {
		h.rows = h.rows[:0]
		for _, r := range h.allRows {
			haystack := strings.ToLower(r.name + "\n" + r.recap + "\n" + r.meta + "\n" + r.id)
			if strings.Contains(haystack, q) {
				h.rows = append(h.rows, r)
			}
		}
	}
	if len(h.rows) == 0 {
		h.cursor = 0
	} else {
		h.cursor = clampInt(h.cursor, 0, len(h.rows)-1)
	}
	h.scrollOffset = 0
	h.clampScroll()
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
	filterLabel := "  / search"
	if h.filtering || h.filter != "" {
		filterLabel = "  search: " + h.filter
		if h.filtering {
			filterLabel += "█"
		}
		filterLabel += fmt.Sprintf("  (%d/%d)", len(h.rows), len(h.allRows))
	}
	add(h.styles.Muted.Render(ansi.Truncate(filterLabel, panelW, "…")), histLineMeta{row: -1})
	add("", histLineMeta{row: -1})

	if len(h.rows) == 0 {
		if strings.TrimSpace(h.filter) != "" {
			add(h.styles.Muted.Render("  (no conversations match the search)"), histLineMeta{row: -1})
			return lines, meta
		}
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

	glyph := "▸ "
	if r.expanded {
		glyph = "▾ "
	}
	// Session titles render in the markdown H1 style (bright amber, bold; see
	// CrackerMarkdownStyle H1). Selection is shown by the arrow colour — lime for
	// the cursor row, muted grey otherwise — so the clickable arrow stays visible.
	titleStyle := h.styles.Bright.Bold(true)
	var arrow string
	if i == h.cursor {
		arrow = h.styles.Accent.Render(glyph)
	} else {
		arrow = h.styles.Muted.Render(glyph)
	}

	// Line 1: " <arrow><name padded>  <meta>" budgeted so meta sits at the right.
	metaCell := h.styles.Muted.Render(r.meta)
	metaW := lipgloss.Width(metaCell)
	const lead = 1 + 2                 // leading space + arrow cell
	nameW := panelW - lead - metaW - 2 // 2-space gap before meta
	if nameW < 8 {
		nameW = 8
	}
	nameTxt := ansi.Truncate(r.name, nameW, "…")
	nameCell := titleStyle.Render(nameTxt) + strings.Repeat(" ", maxInt(0, nameW-lipgloss.Width(nameTxt)))
	line1 := " " + arrow + nameCell + "  " + metaCell
	add(line1, histLineMeta{row: i, arrowCell: true})

	indent := "      "
	// Line 2 (collapsed only): one-line recap preview. When the row is expanded,
	// the drawer below renders the full recap, so skip the preview to avoid
	// showing the recap twice.
	if !r.expanded {
		recap := r.recap
		if strings.TrimSpace(recap) == "" {
			recap = "(no recap)"
		}
		recapTxt := ansi.Truncate(recap, maxInt(8, panelW-lipgloss.Width(indent)), "…")
		add(indent+h.styles.Primary.Render(recapTxt), histLineMeta{row: i})
	}

	if r.expanded {
		panelInner := panelW
		// Full recap wrapped, indented under the row.
		recapFull := strings.TrimSpace(r.recap)
		if recapFull == "" {
			recapFull = "(no recap)"
		}
		for _, l := range strings.Split(ansi.Wrap(recapFull, maxInt(8, panelInner-lipgloss.Width(indent)), ""), "\n") {
			add(indent+h.styles.Muted.Render(l), histLineMeta{row: i})
		}
		if !r.turnsLoaded {
			add(indent+h.styles.Muted.Render("loading…"), histLineMeta{row: i})
		} else if len(r.turns) > 0 {
			add(indent+h.styles.Info.Render("recent:"), histLineMeta{row: i})
			for _, l := range historyTailLines(r.turns, 3, panelInner, h.styles) {
				add(l, histLineMeta{row: i})
			}
		}
	}
}

// expandSelected marks the selected row expanded and, if its turns aren't loaded
// yet, returns a Cmd that fetches them.
func (h *historyView) expandSelected() tea.Cmd {
	if h.cursor < 0 || h.cursor >= len(h.rows) {
		return nil
	}
	h.rows[h.cursor].expanded = true
	r := h.rows[h.cursor]
	if r.turnsLoaded || h.agent == nil {
		return nil
	}
	id := r.id
	ag := h.agent
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		turns, err := ag.GetConversationTurns(ctx, id)
		if err != nil {
			turns = nil
		}
		return historyTurnsLoadedMsg{id: id, turns: turns}
	}
}

// historyTurnsLoadedMsg carries lazily-fetched turns back to the model, which
// routes them to applyTurns.
type historyTurnsLoadedMsg struct {
	id    string
	turns []agentclient.ContextTurn
}

func (h *historyView) applyTurns(id string, turns []agentclient.ContextTurn) {
	for i := range h.rows {
		if h.rows[i].id == id {
			h.rows[i].turns = turns
			h.rows[i].turnsLoaded = true
		}
	}
	for i := range h.allRows {
		if h.allRows[i].id == id {
			h.allRows[i].turns = turns
			h.allRows[i].turnsLoaded = true
		}
	}
}

// historyTailLines renders the last n PROSE turns (tool_use/tool_result skipped)
// as indented "role · preview" lines, each clipped to width.
func historyTailLines(turns []agentclient.ContextTurn, n, width int, styles theme.Styles) []string {
	var prose []agentclient.ContextTurn
	for _, t := range turns {
		if t.Kind == "tool_use" || t.Kind == "tool_result" {
			continue
		}
		prose = append(prose, t)
	}
	if len(prose) > n {
		prose = prose[len(prose)-n:]
	}
	indent := "        "
	w := maxInt(8, width-lipgloss.Width(indent))
	out := make([]string, 0, len(prose))
	for _, t := range prose {
		line := t.Role + " · " + strings.TrimSpace(t.Preview)
		out = append(out, indent+styles.Muted.Render(ansi.Truncate(line, w, "…")))
	}
	return out
}

func (h *historyView) View() string {
	lines, _ := h.rowsLines()
	height := dashboardContentHeight(h.height)
	h.scrollOffset = clampInt(h.scrollOffset, 0, maxInt(0, len(lines)-height))
	return renderScrollable(lines, height, dashboardPanelWidth(h.width), h.scrollOffset, h.styles)
}

// handleClick handles a left-click on the history panel. yLocal is the click
// row relative to the page's top content row (i.e. mouse.Y - contentTop()).
// If the click lands on a row's arrowCell and x is within the arrow columns
// (x <= 2), the row is selected and its expansion is toggled. Collapsing
// returns (nil, true); expanding returns (expandSelected(), true). Any other
// click returns (nil, false).
func (h *historyView) handleClick(x, yLocal int) (tea.Cmd, bool) {
	if yLocal < 0 {
		return nil, false
	}
	_, meta := h.rowsLines()
	idx := h.scrollOffset + yLocal
	if idx < 0 || idx >= len(meta) {
		return nil, false
	}
	m := meta[idx]
	if m.row < 0 || !m.arrowCell || x > 2 {
		return nil, false
	}
	h.cursor = m.row
	if h.rows[m.row].expanded {
		h.rows[m.row].expanded = false
		return nil, true
	}
	return h.expandSelected(), true
}

// --- scroller ---

func (h *historyView) ScrollBy(delta int)  { h.scrollOffset += delta; h.clampScroll() }
func (h *historyView) ScrollTo(offset int) { h.scrollOffset = offset; h.clampScroll() }
func (h *historyView) ScrollState() contentPageScrollState {
	total := h.lineCount()
	height := dashboardContentHeight(h.height)
	return contentPageScrollState{Total: total, Height: height, Offset: clampInt(h.scrollOffset, 0, maxInt(0, total-height))}
}
func (h *historyView) clampScroll()   { h.scrollOffset = h.ScrollState().Offset }
func (h *historyView) lineCount() int { l, _ := h.rowsLines(); return len(l) }
