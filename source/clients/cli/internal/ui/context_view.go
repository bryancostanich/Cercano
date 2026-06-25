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
	cv := &contextView{palette: p, styles: s, agent: ag, convID: convID, width: w, height: h}
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
	turnsBlock := c.renderScrollableContent(strings.Join(c.turnsLines(), "\n"), turnsH)
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

// turnsLines renders the header + the turns list (or the empty/error states).
func (c *contextView) turnsLines() []string {
	lines := []string{c.renderHeader(), ""}
	switch {
	case c.convID == "":
		lines = append(lines, c.styles.Muted.Render("no conversation yet"))
	case c.snapshot.TurnsErr != nil:
		lines = append(lines, c.styles.Error.Render("turns unavailable: "+c.snapshot.TurnsErr.Error()))
	case len(c.snapshot.Turns) == 0:
		lines = append(lines, c.styles.Muted.Render("context is empty"))
	default:
		for i, t := range c.snapshot.Turns {
			lines = append(lines, c.renderTurn(i, t))
		}
	}
	return lines
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

func (c *contextView) renderTurn(i int, t agentclient.ContextTurn) string {
	if c.markedForDelete(t.ID) {
		badge := c.styles.Error.Render("✗ [" + t.Role + "]")
		toks := c.styles.Dim.Render(fmt.Sprintf("≈%s", formatTokens(t.EstTokens)))
		return c.styles.Dim.Render(fmt.Sprintf("%s %s  %s", badge, toks, t.Preview))
	}
	badge := c.styles.Info.Render("[" + t.Role + "]")
	switch t.Kind {
	case "tool_use", "tool_result":
		badge = c.styles.Muted.Render("[" + t.Kind + "]")
	}
	toks := c.styles.Muted.Render(fmt.Sprintf("≈%s", formatTokens(t.EstTokens)))
	return fmt.Sprintf("%s %s  %s", badge, toks, t.Preview)
}

// --- scroller (mirrors runtimeDashboard) ---

func (c *contextView) ScrollBy(delta int) { c.scrollOffset += delta; c.clampScroll() }
func (c *contextView) ScrollTo(offset int) { c.scrollOffset = offset; c.clampScroll() }
func (c *contextView) ScrollState() contentPageScrollState {
	turnsH, _ := c.regionHeights()
	total := len(c.turnsLines())
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
		b.WriteString(ansi.Truncate(line, panelW, ""))
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
