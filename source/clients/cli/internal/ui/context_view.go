package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

type contextSnapshot struct {
	Turns    []agentclient.ContextTurn
	TurnsErr error
	Usage    *agentclient.ContextUsage
	UsageErr error
}

type contextView struct {
	width, height   int
	palette         theme.Palette
	styles          theme.Styles
	agent           *agentclient.Client
	convID          string
	snapshot        contextSnapshot
	scrollOffset    int
	showingProposal bool
	proposal        agentclient.Proposal
	expanded        map[string]bool
	focusedTurn     int

	driver *contextManagerDriver
	pane   *chatPane
}

func loadContextSnapshot(ag *agentclient.Client, convID string) contextSnapshot {
	var snap contextSnapshot
	if ag == nil || convID == "" {
		return snap
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snap.Turns, snap.TurnsErr = ag.GetConversationTurns(ctx, convID)
	snap.Usage, snap.UsageErr = ag.GetContextUsage(ctx, convID)
	return snap
}

// contextSnapshotMsg carries an async-loaded snapshot back to the model.
type contextSnapshotMsg struct{ snap contextSnapshot }

// contextRefreshTickMsg drives /c auto-refresh while the page is open.
type contextRefreshTickMsg struct{}

// loadContextSnapshotCmd loads the snapshot off the UI thread (the load can block
// up to 2s on the RPCs, so it must not run inline).
func loadContextSnapshotCmd(ag *agentclient.Client, convID string) tea.Cmd {
	return func() tea.Msg { return contextSnapshotMsg{snap: loadContextSnapshot(ag, convID)} }
}

// contextRefreshTick schedules the next /c auto-refresh.
func contextRefreshTick() tea.Cmd {
	return tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg { return contextRefreshTickMsg{} })
}

func newContextView(ag *agentclient.Client, p theme.Palette, s theme.Styles, convID string, w, h int) (*contextView, tea.Cmd) {
	cv := &contextView{palette: p, styles: s, agent: ag, convID: convID, width: w, height: h, expanded: map[string]bool{}, focusedTurn: -1}
	cv.snapshot = loadContextSnapshot(ag, convID)
	cv.driver = &contextManagerDriver{
		agent:  ag,
		convID: convID,
		onDeleted: func(ids []string) {
			cv.snapshot = loadContextSnapshot(ag, convID)
			cv.cancelProposal()
		},
		mark:   cv.applyProposalIDs,
		unmark: cv.cancelProposal,
	}
	cv.pane = newChatPane(cv.driver, s, p, w, h)
	return cv, nil
}

func (c *contextView) ID() contentPageID { return contentPageContext }

func (c *contextView) SetSize(w, h int) {
	c.width = w
	c.height = h
	if c.pane != nil {
		c.pane.SetSize(w, h)
	}
	c.clampScroll()
}

// Update handles keys that the model hasn't intercepted. For *contextView,
// the model's handleContextViewKey owns most keys; this method handles the
// residual scroll + close keys so the contentPage contract stays valid.
func (c *contextView) Update(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc", "q":
		return nil, true
	case "r":
		c.snapshot = loadContextSnapshot(c.agent, c.convID)
		return nil, false
	case "up", "k":
		c.ScrollBy(-1)
	case "down", "j":
		c.ScrollBy(1)
	case "pgup", "ctrl+b":
		c.ScrollBy(-dashboardContentHeight(c.height))
	case "pgdown", "ctrl+f":
		c.ScrollBy(dashboardContentHeight(c.height))
	case "ctrl+u":
		c.ScrollBy(-maxInt(1, dashboardContentHeight(c.height)/2))
	case "ctrl+d":
		c.ScrollBy(maxInt(1, dashboardContentHeight(c.height)/2))
	}
	return nil, false
}

// --- proposal state methods ---

func (c *contextView) applyProposal(p agentclient.Proposal) {
	c.proposal = p
	c.showingProposal = true
}

func (c *contextView) cancelProposal() {
	c.proposal = agentclient.Proposal{}
	c.showingProposal = false
}

// applyProposalIDs marks the given turn IDs for deletion (used by the driver's
// mark hook so turns render with ✗ while a confirm is pending).
func (c *contextView) applyProposalIDs(ids []string) {
	c.proposal.DeleteIDs = ids
	c.showingProposal = true
}

func (c *contextView) markedForDelete(id string) bool {
	if !c.showingProposal {
		return false
	}
	for _, d := range c.proposal.DeleteIDs {
		if d == id {
			return true
		}
	}
	return false
}

// (proposeCmd and deleteCmd removed — the contextManagerDriver owns those RPCs now.)

// View renders two stacked regions — the turns list (top) and the chat pane
// (bottom) — each with its OWN scrollbar. The pane is sized to the height it
// needs (capped at half the panel) instead of being embedded inside the turns
// scroller, which previously nested a second scrollbar and let an empty pane eat
// the panel.
func (c *contextView) View() string {
	turnsH, paneH := c.regionHeights()
	turnsBlock := c.renderScrollableContent(strings.Join(c.turnsLinesOnly(), "\n"), turnsH)
	paneBlock := padLines("", paneH)
	if c.pane != nil {
		c.pane.SetSize(c.width, paneH)
		paneBlock = padLines(c.pane.View(), paneH)
	}
	return turnsBlock + "\n" + paneBlock
}

// regionHeights splits the content area: the chat pane takes what it needs
// (capped at half), the turns list gets the rest (at least one row).
func (c *contextView) regionHeights() (turnsH, paneH int) {
	totalH := dashboardContentHeight(c.height)
	if totalH < 2 {
		totalH = 2
	}
	paneH = 1
	if c.pane != nil {
		c.pane.SetSize(c.width, totalH) // set width so DesiredHeight wraps correctly
		paneH = clampInt(c.pane.DesiredHeight(), 1, maxInt(1, totalH/2))
	}
	turnsH = totalH - paneH
	if turnsH < 1 {
		turnsH = 1
	}
	return turnsH, paneH
}

// turnLineMeta describes each line emitted by turnsLines for hit-testing (Task 3).
type turnLineMeta struct {
	turnID    string
	header    bool // first line of the turn (carries the arrow)
	arrowCell bool // header line of an expandable turn (clickable)
}

// turnsLines renders the header + turns list and returns parallel line/meta slices.
func (c *contextView) turnsLines() ([]string, []turnLineMeta) {
	var lines []string
	var meta []turnLineMeta
	add := func(s string, m turnLineMeta) { lines = append(lines, s); meta = append(meta, m) }
	add(c.renderHeader(), turnLineMeta{})
	add("", turnLineMeta{})
	switch {
	case c.convID == "":
		add(c.styles.Muted.Render("no conversation yet"), turnLineMeta{})
	case c.snapshot.TurnsErr != nil:
		add(c.styles.Error.Render("turns unavailable: "+c.snapshot.TurnsErr.Error()), turnLineMeta{})
	case len(c.snapshot.Turns) == 0:
		add(c.styles.Muted.Render("context is empty"), turnLineMeta{})
	default:
		for i, t := range c.snapshot.Turns {
			c.appendTurn(&lines, &meta, i, t)
		}
	}
	return lines, meta
}

// turnsLinesOnly returns only the lines (discarding meta); used by View/ScrollState.
func (c *contextView) turnsLinesOnly() []string { l, _ := c.turnsLines(); return l }

// toggleExpand toggles the expanded state for a turn by ID.
func (c *contextView) toggleExpand(id string) {
	if c.expanded == nil {
		c.expanded = map[string]bool{}
	}
	c.expanded[id] = !c.expanded[id]
}

// turnBodyLines returns the body wrapped to content width (falls back to Preview).
func (c *contextView) turnBodyLines(t agentclient.ContextTurn) []string {
	body := t.Body
	if strings.TrimSpace(body) == "" {
		body = t.Preview
	}
	w := dashboardPanelWidth(c.width) - 4 // room for arrow + token gutter
	if w < 8 {
		w = 8
	}
	return strings.Split(ansi.Wrap(body, w, ""), "\n")
}

// collapsedCount returns the number of body lines shown when a turn is collapsed.
func (c *contextView) collapsedCount(t agentclient.ContextTurn) int {
	if t.Role == "assistant" {
		return 3
	}
	return 1
}

// turnExpandable reports whether a turn should show an expand arrow.
func (c *contextView) turnExpandable(t agentclient.ContextTurn) bool {
	if t.Truncated {
		return true
	}
	return len(c.turnBodyLines(t)) > c.collapsedCount(t)
}

// appendTurn pushes the rendered lines + meta for one turn.
func (c *contextView) appendTurn(lines *[]string, meta *[]turnLineMeta, i int, t agentclient.ContextTurn) {
	add := func(s string, m turnLineMeta) { *lines = append(*lines, s); *meta = append(*meta, m) }

	bodyLines := c.turnBodyLines(t)
	expandable := c.turnExpandable(t)
	open := c.expanded[t.ID]

	shown := bodyLines
	if !open {
		limit := c.collapsedCount(t)
		if limit > len(bodyLines) {
			limit = len(bodyLines)
		}
		shown = bodyLines[:limit]
	}

	// Arrow gutter
	arrow := "  "
	if expandable {
		if open {
			arrow = "▾ "
		} else {
			arrow = "▸ "
		}
	}

	// Build the header line
	if c.markedForDelete(t.ID) {
		badge := c.styles.Error.Render("✗ [" + t.Role + "]")
		toks := c.styles.Dim.Render(fmt.Sprintf("≈%s", formatTokens(t.EstTokens)))
		firstLine := ""
		if len(shown) > 0 {
			firstLine = shown[0]
		}
		header := c.styles.Dim.Render(fmt.Sprintf("%s%s %s  %s", arrow, badge, toks, firstLine))
		add(header, turnLineMeta{turnID: t.ID, header: true, arrowCell: expandable})
	} else {
		badge := c.styles.Info.Render("[" + t.Role + "]")
		switch t.Kind {
		case "tool_use", "tool_result":
			badge = c.styles.Muted.Render("[" + t.Kind + "]")
		}
		if i == c.focusedTurn {
			badge = c.styles.Bright.Render("[" + t.Role + "]")
		}
		toks := c.styles.Muted.Render(fmt.Sprintf("≈%s", formatTokens(t.EstTokens)))
		firstLine := ""
		if len(shown) > 0 {
			firstLine = shown[0]
		}
		header := fmt.Sprintf("%s%s %s  %s", arrow, badge, toks, firstLine)
		add(header, turnLineMeta{turnID: t.ID, header: true, arrowCell: expandable})
	}

	// Remaining shown lines with hang-indent
	if len(shown) > 1 {
		for _, l := range shown[1:] {
			add("    "+l, turnLineMeta{turnID: t.ID})
		}
	}

	// Truncated indicator when expanded
	if open && t.Truncated {
		add(c.styles.Dim.Render("    …(truncated)"), turnLineMeta{turnID: t.ID})
	}
}

// padToWidth right-pads s with spaces to exactly w visible columns (ANSI-aware),
// so a scrollbar glyph appended after it lands at a fixed far-right column rather
// than at the row's text-end. Assumes s is already truncated to <= w.
func padToWidth(s string, w int) string {
	if pad := w - ansi.StringWidth(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// padLines forces s to exactly n lines (pad with blanks / truncate) so a region
// fills its allotted band height precisely.
func padLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	for len(lines) < n {
		lines = append(lines, "")
	}
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func (c *contextView) renderHeader() string {
	if c.snapshot.UsageErr != nil || c.snapshot.Usage == nil {
		return c.styles.Muted.Render("context  usage unavailable")
	}
	u := c.snapshot.Usage
	pct := int(u.Percent*100 + 0.5)
	bar := renderMeterBar(u.Percent, 10, c.styles)
	return fmt.Sprintf("%s  %s / %s  · %d%%  %s",
		c.styles.Bright.Render("context"),
		formatThousands(u.TokensUsed), formatThousands(u.ModelMax), pct, bar)
}


// handleClick toggles a turn's expansion when the click lands on its arrow cell.
// yLocal is the click row relative to the page's top content row (i.e. after
// subtracting m.contentTop() from the raw mouse Y).
func (c *contextView) handleClick(x, yLocal int) bool {
	turnsH, _ := c.regionHeights()
	if yLocal < 0 || yLocal >= turnsH {
		return false // outside the turns region
	}
	_, meta := c.turnsLines()
	idx := c.scrollOffset + yLocal
	if idx < 0 || idx >= len(meta) {
		return false
	}
	m := meta[idx]
	if m.arrowCell && x <= 1 { // arrow occupies the leading 1-2 columns
		c.toggleExpand(m.turnID)
		return true
	}
	return false
}

// focusNextExpandable advances focusedTurn by dir (+1 or -1) to the next index
// where turnExpandable returns true, wrapping around. No-op if no expandable turns.
func (c *contextView) focusNextExpandable(dir int) {
	n := len(c.snapshot.Turns)
	if n == 0 {
		return
	}
	for step := 1; step <= n; step++ {
		i := (c.focusedTurn + dir*step + n*step) % n
		if i < 0 {
			i += n
		}
		if c.turnExpandable(c.snapshot.Turns[i]) {
			c.focusedTurn = i
			return
		}
	}
}

// --- scroller (mirrors runtimeDashboard) ---

func (c *contextView) ScrollBy(delta int) { c.scrollOffset += delta; c.clampScroll() }
func (c *contextView) ScrollTo(offset int) { c.scrollOffset = offset; c.clampScroll() }
func (c *contextView) ScrollState() contentPageScrollState {
	turnsH, _ := c.regionHeights()
	total := len(c.turnsLinesOnly())
	return contentPageScrollState{Total: total, Height: turnsH, Offset: clampInt(c.scrollOffset, 0, maxInt(0, total-turnsH))}
}
func (c *contextView) clampScroll() { c.scrollOffset = c.ScrollState().Offset }

func (c *contextView) renderScrollableContent(full string, height int) string {
	if height < 1 {
		height = 1
	}
	lines := strings.Split(full, "\n")
	c.scrollOffset = clampInt(c.scrollOffset, 0, maxInt(0, len(lines)-height))
	panelW := dashboardPanelWidth(c.width)
	col := scrollbarColumn(len(lines), height, c.scrollOffset)
	var b strings.Builder
	for i := 0; i < height; i++ {
		line := ""
		if src := c.scrollOffset + i; src >= 0 && src < len(lines) {
			line = lines[src]
		}
		b.WriteString(padToWidth(ansi.Truncate(line, panelW, ""), panelW))
		b.WriteString(" ")
		if i < len(col) {
			switch col[i] {
			case '█':
				b.WriteString(c.styles.Border.Render("█"))
			case '░':
				b.WriteString(c.styles.BorderDim.Render("░"))
			default:
				b.WriteString(" ")
			}
		} else {
			b.WriteString(" ")
		}
		if i < height-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// --- small formatters ---

func formatThousands(n int) string {
	s := fmt.Sprintf("%d", n)
	if n < 1000 {
		return s
	}
	var out []byte
	for i, ch := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, ch)
	}
	return string(out)
}

func renderMeterBar(pct float64, width int, s theme.Styles) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	filled := int(pct*float64(width) + 0.5)
	style := s.Accent
	switch {
	case pct >= 0.9:
		style = s.Error
	case pct >= 0.7:
		style = s.Warn
	}
	return style.Render(strings.Repeat("█", filled)) + s.Dim.Render(strings.Repeat("░", width-filled))
}
