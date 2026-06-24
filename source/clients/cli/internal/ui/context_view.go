package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
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

type contextViewMode int

const (
	cvBrowse   contextViewMode = iota
	cvEditing                  // text input active
	cvProposal                 // proposal shown, awaiting y/n
)

type contextEditProposalMsg struct {
	p   agentclient.Proposal
	err error
}

type contextEditDeletedMsg struct {
	n   int
	err error
}

type contextView struct {
	width, height int
	palette       theme.Palette
	styles        theme.Styles
	agent         *agentclient.Client
	convID        string
	snapshot      contextSnapshot
	scrollOffset  int
	mode          contextViewMode
	input         textinput.Model
	proposal      agentclient.Proposal
	editErr       string
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

func newContextView(ag *agentclient.Client, p theme.Palette, s theme.Styles, convID string, w, h int) (*contextView, tea.Cmd) {
	cv := &contextView{palette: p, styles: s, agent: ag, convID: convID, width: w, height: h}
	cv.snapshot = loadContextSnapshot(ag, convID)
	inp := textinput.New()
	inp.Placeholder = "instruction, e.g. 'drop the debugging tangent'"
	inp.CharLimit = 0
	inp.SetWidth(w - 4)
	cv.input = inp
	return cv, nil
}

func (c *contextView) ID() contentPageID { return contentPageContext }

func (c *contextView) SetSize(w, h int) {
	c.width = w
	c.height = h
	c.clampScroll()
}

func (c *contextView) Update(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch c.mode {
	case cvEditing:
		switch msg.String() {
		case "enter":
			instr := strings.TrimSpace(c.input.Value())
			if instr == "" {
				return nil, false
			}
			c.input.Reset()
			return c.proposeCmd(instr), false
		case "esc":
			c.mode = cvBrowse
			c.editErr = ""
			c.input.Blur()
			return nil, false
		}
		var cmd tea.Cmd
		c.input, cmd = c.input.Update(msg)
		return cmd, false

	case cvProposal:
		switch msg.String() {
		case "y":
			return c.deleteCmd(c.proposal.DeleteIDs), false
		case "n", "esc":
			c.cancelProposal()
			return nil, false
		}
		return nil, false

	default: // cvBrowse — existing 3a handling plus e
		switch msg.String() {
		case "esc", "q":
			return nil, true
		case "e":
			c.mode = cvEditing
			return c.input.Focus(), false
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
}

// --- edit mode small methods ---

func (c *contextView) applyProposal(p agentclient.Proposal) {
	c.proposal = p
	c.mode = cvProposal
	c.editErr = ""
}

func (c *contextView) cancelProposal() {
	c.proposal = agentclient.Proposal{}
	c.mode = cvBrowse
}

func (c *contextView) markedForDelete(id string) bool {
	if c.mode != cvProposal {
		return false
	}
	for _, d := range c.proposal.DeleteIDs {
		if d == id {
			return true
		}
	}
	return false
}

// --- async commands ---

func (c *contextView) proposeCmd(instruction string) tea.Cmd {
	ag, convID := c.agent, c.convID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		p, err := ag.ProposeContextEdit(ctx, convID, instruction)
		return contextEditProposalMsg{p: p, err: err}
	}
}

func (c *contextView) deleteCmd(ids []string) tea.Cmd {
	ag, convID := c.agent, c.convID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		n, err := ag.DeleteConversationTurns(ctx, convID, ids)
		return contextEditDeletedMsg{n: n, err: err}
	}
}

// called by model.go when async msgs arrive

func (c *contextView) onProposal(m contextEditProposalMsg) {
	if m.err != nil {
		c.editErr = "could not interpret that — try rephrasing"
		c.mode = cvEditing
		return
	}
	c.applyProposal(m.p)
}

func (c *contextView) onDeleted(m contextEditDeletedMsg) tea.Cmd {
	c.cancelProposal()
	c.snapshot = loadContextSnapshot(c.agent, c.convID)
	return nil
}

func (c *contextView) View() string {
	full, contentH := c.fullContent()
	return c.renderScrollableContent(full, contentH)
}

func (c *contextView) fullContent() (string, int) {
	var lines []string
	lines = append(lines, c.renderHeader())
	lines = append(lines, "")
	if c.convID == "" {
		lines = append(lines, c.styles.Muted.Render("no conversation yet"))
	} else if c.snapshot.TurnsErr != nil {
		lines = append(lines, c.styles.Error.Render("turns unavailable: "+c.snapshot.TurnsErr.Error()))
	} else if len(c.snapshot.Turns) == 0 {
		lines = append(lines, c.styles.Muted.Render("context is empty"))
	} else {
		for i, t := range c.snapshot.Turns {
			lines = append(lines, c.renderTurn(i, t))
		}
	}
	lines = append(lines, "")
	lines = append(lines, c.renderFooter())
	return strings.Join(lines, "\n"), dashboardContentHeight(c.height)
}

func (c *contextView) renderFooter() string {
	switch c.mode {
	case cvProposal:
		rationale := c.styles.Warn.Render(c.proposal.Rationale)
		confirm := c.styles.Bright.Render("[y]") + " delete  " + c.styles.Bright.Render("[n]") + " cancel"
		return rationale + "\n" + confirm
	case cvEditing:
		hint := c.styles.Muted.Render("enter to propose · esc to cancel")
		errLine := ""
		if c.editErr != "" {
			errLine = "\n" + c.styles.Error.Render(c.editErr)
		}
		return c.input.View() + "\n" + hint + errLine
	default: // cvBrowse
		return c.styles.Muted.Render("r: refresh · e: edit · esc/q: back")
	}
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
	full, contentH := c.fullContent()
	total := countLines([]string{full})
	return contentPageScrollState{Total: total, Height: contentH, Offset: clampInt(c.scrollOffset, 0, maxInt(0, total-contentH))}
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
