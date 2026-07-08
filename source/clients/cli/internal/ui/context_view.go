package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

type contextSnapshot struct {
	Turns      []agentclient.ContextTurn
	TurnsErr   error
	Usage      *agentclient.ContextUsage
	UsageErr   error
	Compaction *agentclient.CompactionState
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
	busyFlag        bool // true while a context-edit turn is in flight

	showOriginal bool
	notice       string

	md     *render.Markdown
	driver *contextManagerDriver
	chat   chatView
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
	snap.Compaction, _ = ag.GetCompactionState(ctx, convID) // best-effort; nil on error
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
	cv := &contextView{palette: p, styles: s, agent: ag, convID: convID, width: w, height: h, expanded: map[string]bool{}, focusedTurn: -1, md: render.NewMarkdown(theme.MarkdownStyle(p))}
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
	cv.chat = newChatView(s, p, "", "", w-2, h)
	return cv, nil
}

func (c *contextView) ID() contentPageID { return contentPageContext }

func (c *contextView) SetSize(w, h int) {
	c.width = w
	c.height = h
	c.chat.SetSize(w-2, h)
	c.clampScroll()
}

// busy reports whether a context-edit turn is in flight. The flag is set by
// submitContextEdit and cleared by chatDoneMsg / chatErrorMsg, so it spans the
// full propose→confirm→done lifecycle rather than just the streaming-placeholder
// window (which ends when chatConfirmMsg fills the placeholder with the rationale).
func (c *contextView) busy() bool { return c.busyFlag }

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
	c.chat.SetSize(c.width-2, paneH)
	// The /c chat band has no scroll handler of its own (up/down move the turns
	// list), so it always follows the bottom — mirroring the old pane's follow=true.
	c.chat.GotoBottom()
	paneBlock := padLines(c.chat.View(), paneH)
	return turnsBlock + "\n" + paneBlock
}

// regionHeights splits the content area: the chat pane takes what it needs
// (capped at half), the turns list gets the rest (at least one row).
func (c *contextView) regionHeights() (turnsH, paneH int) {
	totalH := dashboardContentHeight(c.height)
	if totalH < 2 {
		totalH = 2
	}
	c.chat.SetSize(c.width-2, totalH) // set width so DesiredHeight wraps correctly
	paneH = clampInt(c.chat.DesiredHeight(), 1, maxInt(1, totalH/2))
	turnsH = totalH - paneH
	if turnsH < 1 {
		turnsH = 1
	}
	return turnsH, paneH
}

// turnLineMeta describes each line emitted by turnsLines for hit-testing (Task 3).
type turnLineMeta struct {
	railCell  bool // body line of an expanded turn: rail gutter click collapses
	turnID    string
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
		turns := c.snapshot.Turns
		cs := c.snapshot.Compaction
		frozenN := c.frozenFloor()
		if frozenN > 0 {
			// Consolidated summary block, then a collapsed marker for the frozen span.
			if cs.ConsolidatedSummary != "" {
				for _, line := range strings.Split(cs.ConsolidatedSummary, "\n") {
					add(c.styles.Muted.Render(line), turnLineMeta{})
				}
			}
			add(c.styles.Dim.Render(fmt.Sprintf("  ▣ %d turns compacted into the summary above  (Ctrl+O: show original)", frozenN)), turnLineMeta{})
			add("", turnLineMeta{})
		}
		for i := frozenN; i < len(turns); i++ {
			c.appendTurn(&lines, &meta, i, turns[i])
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

// expandedBodyLines renders a turn's full body for the expanded view. Prose
// turns go through the markdown engine; tool_use/tool_result bodies go through
// the shared tool-body renderer (JSON pretty-printed in a fence, markdown
// rendered, raw output kept verbatim).
func (c *contextView) expandedBodyLines(t agentclient.ContextTurn) []string {
	w := dashboardPanelWidth(c.width) - 8 // arrow + token gutter + 4-space indent
	if w < 8 {
		w = 8
	}
	switch t.Kind {
	case "tool_use":
		// Edit/Write args render as the same +/- diff the main chat shows;
		// other tools fall back to the shared body renderer (JSON fence,
		// markdown, verbatim).
		if diff := renderToolArgsDiff(t.ToolName, t.ToolArgs, c.startLineFor(t.ToolUseID), w, c.styles); diff != nil {
			return diff
		}
		return renderToolBody(t.Body, "", c.md, w)
	case "tool_result":
		// Correlate back to the originating tool_use so results render
		// through the main chat's path — syntax highlight inferred from the
		// call's args, JSON/markdown/plain sniffing otherwise. "" declared
		// type → sniff; the real-content-type (c) signal slots in here once
		// the agent carries a type on tool results.
		if name, args, ok := c.toolUseFor(t.ToolUseRef); ok {
			return renderToolResultBody(name, args, t.Body, c.md, w)
		}
		return renderToolBody(t.Body, "", c.md, w)
	}
	return strings.Split(c.md.Render(strings.TrimSpace(t.Body), w), "\n")
}

// toolUseFor finds the tool_use turn matching a tool_result's ref and returns
// its tool name + args JSON. Linear scan — turn counts are small and this only
// runs while rendering an expanded turn.
// startLineFor returns the agent-recorded start line for a call's edit/write,
// read from the correlated tool_result turn (the result block carries it).
// 0 when the result hasn't landed or the call wasn't a file edit/write.
func (c *contextView) startLineFor(toolUseID string) int {
	if toolUseID == "" {
		return 0
	}
	for _, u := range c.snapshot.Turns {
		if u.ToolUseRef == toolUseID {
			return u.ToolStartLine
		}
	}
	return 0
}

func (c *contextView) toolUseFor(ref string) (name, args string, ok bool) {
	if ref == "" {
		return "", "", false
	}
	for _, u := range c.snapshot.Turns {
		if u.ToolUseID == ref {
			return u.ToolName, u.ToolArgs, true
		}
	}
	return "", "", false
}

// appendTurn pushes the rendered lines + meta for one turn.
func (c *contextView) appendTurn(lines *[]string, meta *[]turnLineMeta, i int, t agentclient.ContextTurn) {
	add := func(s string, m turnLineMeta) { *lines = append(*lines, s); *meta = append(*meta, m) }

	bodyLines := c.turnBodyLines(t)
	expandable := t.Truncated || len(bodyLines) > c.collapsedCount(t)
	open := c.expanded[t.ID]
	deleted := c.markedForDelete(t.ID)

	// Expanded text turns render their full body through the markdown engine and
	// keep the metadata (badge + tokens) on a header line of its own. All other
	// states keep the compact preview with the first body line on the header.
	richExpanded := open && !deleted && c.md != nil && strings.TrimSpace(t.Body) != ""

	shown := bodyLines
	switch {
	case richExpanded:
		shown = c.expandedBodyLines(t)
	case !open:
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

	// Badge + token count.
	var badge, toks string
	if deleted {
		badge = c.styles.Error.Render("✗ [" + t.Role + "]")
		toks = c.styles.Dim.Render(fmt.Sprintf("≈%s", formatTokens(t.EstTokens)))
	} else {
		badge = c.styles.Info.Render("[" + t.Role + "]")
		switch t.Kind {
		case "tool_use":
			label := t.Kind
			if t.ToolName != "" {
				label = t.ToolName
			}
			badge = c.styles.Muted.Render("[" + label + "]")
		case "tool_result":
			label := t.Kind
			if name, _, ok := c.toolUseFor(t.ToolUseRef); ok && name != "" {
				label = "→ " + name
			}
			badge = c.styles.Muted.Render("[" + label + "]")
		}
		if i == c.focusedTurn {
			badge = c.styles.Bright.Render("[" + t.Role + "]")
		}
		toks = c.styles.Muted.Render(fmt.Sprintf("≈%s", formatTokens(t.EstTokens)))
	}

	// The header carries the first preview line only when NOT markdown-expanded.
	firstLine := ""
	bodyStart := 0
	if !richExpanded && len(shown) > 0 {
		firstLine = shown[0]
		bodyStart = 1
	}
	header := fmt.Sprintf("%s%s %s", arrow, badge, toks)
	if firstLine != "" {
		header += "  " + firstLine
	}
	if deleted {
		header = c.styles.Dim.Render(header)
	}
	add(header, turnLineMeta{turnID: t.ID, arrowCell: expandable})

	// Body lines below the header, hang-indented. Expanded turns get the same
	// collapse rail as the main chat's tool expand: a faint │ down the left
	// edge (╰ hook on the last line), and a click anywhere on that gutter
	// collapses the turn — no scrolling back up to the arrow. The indent stays
	// PLAIN (outside any style) so overlayRail finds a real space at offset 2.
	body := make([]string, 0, len(shown[bodyStart:])+1)
	for _, l := range shown[bodyStart:] {
		body = append(body, "    "+l)
	}
	if open && t.Truncated {
		body = append(body, "    "+c.styles.Dim.Render("…(truncated)"))
	}
	railed := open && !deleted && len(body) > 0
	if railed {
		for k := range body {
			g := "│"
			if k == len(body)-1 {
				g = "╰"
			}
			body[k] = overlayRail(body[k], 2, g)
		}
	}
	for _, l := range body {
		add(l, turnLineMeta{turnID: t.ID, railCell: railed})
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
	head := fmt.Sprintf("%s  %s / %s  · %d%%  %s",
		c.styles.Bright.Render("context"),
		formatThousands(u.TokensUsed), formatThousands(u.ModelMax), pct, bar)

	cs := c.snapshot.Compaction
	if cs != nil && cs.FrozenTurns > 0 {
		saved := 0
		if cs.RawTokens > 0 {
			saved = int(100 * (1 - float64(cs.SentTokens)/float64(cs.RawTokens)))
		}
		badge := c.styles.Muted.Render(fmt.Sprintf("  ·  ▣ %d%%↓  ·  %d compacted · %d live", saved, cs.FrozenTurns, cs.LiveTurns))
		if cs.Compacting {
			badge = c.styles.Accent.Render("  ·  compacting…")
		}
		head += badge
	}
	if c.notice != "" {
		head += "\n" + c.styles.Accent.Render(c.notice)
	}
	return head
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
	// Rail gutter on an expanded turn's body: the │/╰ sits at column 2 inside
	// the 4-space indent, so claim [0,4) — content starts at column 4 and
	// stays selectable.
	if m.railCell && x <= 3 {
		c.toggleExpand(m.turnID)
		return true
	}
	return false
}

// setFocusedExpanded opens (open=true) or collapses (false) the focused turn —
// the right/left arrow path. No-op when nothing is focused.
func (c *contextView) setFocusedExpanded(open bool) {
	if c.focusedTurn < 0 || c.focusedTurn >= len(c.snapshot.Turns) {
		return
	}
	if c.expanded == nil {
		c.expanded = map[string]bool{}
	}
	c.expanded[c.snapshot.Turns[c.focusedTurn].ID] = open
}

// focusNextExpandable advances focusedTurn by dir (+1 or -1) to the next index
// where turnExpandable returns true, wrapping around. No-op if no expandable turns.
// From the neutral start (focusedTurn == -1): forward lands on the first expandable,
// backward lands on the last expandable.
// frozenFloor is the index of the first turn actually RENDERED in the current
// view. In the sent view the frozen prefix is hidden (it lives in the summary),
// so focus/iteration must start here; in the original view (or with no
// compaction) it's 0. turnsLines and focusNextExpandable share this so the
// focusable range can never drift from the rendered range.
func (c *contextView) frozenFloor() int {
	cs := c.snapshot.Compaction
	if c.showOriginal || cs == nil || cs.FrozenTurns <= 0 {
		return 0
	}
	if cs.FrozenTurns > len(c.snapshot.Turns) {
		return len(c.snapshot.Turns)
	}
	return cs.FrozenTurns
}

func (c *contextView) focusNextExpandable(dir int) {
	n := len(c.snapshot.Turns)
	lo := c.frozenFloor() // never focus a hidden frozen turn
	span := n - lo
	if span <= 0 {
		return
	}
	start := c.focusedTurn
	if start < lo {
		if dir > 0 {
			start = lo - 1 // first step gives index lo
		} else {
			start = n // first step gives index n-1
		}
	}
	js := start - lo
	for step := 1; step <= span; step++ {
		i := lo + (((js+dir*step)%span)+span)%span
		if c.turnExpandable(c.snapshot.Turns[i]) {
			c.focusedTurn = i
			return
		}
	}
}

// --- scroller (mirrors runtimeDashboard) ---

func (c *contextView) ScrollBy(delta int)  { c.scrollOffset += delta; c.clampScroll() }
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
	return renderScrollable(lines, height, dashboardPanelWidth(c.width), c.scrollOffset, c.styles)
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
