package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// mcpDashboard is the MCP config tab: a live, flat-list dashboard of hosted
// MCP servers with per-row reconnect/remove actions and an add-server popover.
//
// Unlike runtimeDashboard (which shares one cursor across several action
// blocks) this page is a single flat list, so it keeps its own small cursor
// and action-message state rather than reusing that machinery.
type mcpDashboard struct {
	agent   *agentclient.Client
	palette theme.Palette
	styles  theme.Styles
	width   int
	height  int

	servers []agentclient.McpServer
	cursor  int
	loaded  bool

	// bodyFocused is false while the config tab strip still owns the keyboard
	// and true once focus has dropped into this page's body. The row cursor
	// marker (▶) is only drawn while bodyFocused, so landing on the MCP tab
	// does not paint a caret before the user has entered the list. The config
	// surface only routes keys to Update once the body is focused, so the
	// first delegated key latches this true.
	bodyFocused bool

	// actionMessage is a transient inline notice (last action outcome / error).
	// It auto-clears a few seconds after it is set. actionMsgGen stamps each
	// message so a scheduled clear only fires for the message it was scheduled
	// for — a newer message resets the timer instead of being wiped early.
	actionMessage string
	actionMsgGen  int

	// popover is the add-server form overlay; nil when closed.
	popover *mcpAddForm

	// details is the read-only server-details overlay; nil when closed.
	details *mcpDetails
}

// --- messages -------------------------------------------------------------

type mcpDashboardRefreshMsg struct{}

type mcpDashboardSnapshotMsg struct {
	servers []agentclient.McpServer
	err     error
}

type mcpDashboardActionMsg struct {
	verb string // "reconnect" | "remove" | "add"
	name string
	err  error
}

// mcpDashboardClearActionMsg clears the inline action notice, but only if its
// generation still matches the current one (a newer message supersedes it).
type mcpDashboardClearActionMsg struct{ gen int }

// actionMessageTTL is how long an action notice lingers before auto-clearing.
const actionMessageTTL = 4 * time.Second

// setActionMessage installs an inline notice and returns a command that clears
// it after actionMessageTTL, tagged with this message's generation so a later
// notice restarts the timer rather than being cleared prematurely.
func (d *mcpDashboard) setActionMessage(s string) tea.Cmd {
	d.actionMessage = s
	d.actionMsgGen++
	gen := d.actionMsgGen
	return tea.Tick(actionMessageTTL, func(time.Time) tea.Msg {
		return mcpDashboardClearActionMsg{gen: gen}
	})
}

// clearActionMessage drops the notice if the scheduled clear still targets the
// current message; a newer message (higher gen) leaves it untouched.
func (d *mcpDashboard) clearActionMessage(gen int) {
	if gen == d.actionMsgGen {
		d.actionMessage = ""
	}
}

// newMcpDashboard builds the page and returns it with an initial load command.
func newMcpDashboard(ag *agentclient.Client, p theme.Palette, s theme.Styles, w, h int) (*mcpDashboard, tea.Cmd) {
	d := &mcpDashboard{
		agent:   ag,
		palette: p,
		styles:  s,
		width:   w,
		height:  h,
	}
	return d, d.loadSnapshotCmd()
}

// refreshTick reschedules a snapshot reload so connecting→ready transitions
// and tool-count updates appear without manual reload. The loop always
// reschedules while the page is open; a single failed fetch must not kill it.
func (d *mcpDashboard) refreshTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return mcpDashboardRefreshMsg{} })
}

// loadSnapshotCmd fetches the server list off the UI goroutine.
func (d *mcpDashboard) loadSnapshotCmd() tea.Cmd {
	ag := d.agent
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		servers, err := ag.ListMcpServers(ctx)
		return mcpDashboardSnapshotMsg{servers: servers, err: err}
	}
}

// refreshSnapshot is invoked by the tick handler; it kicks a reload and
// reschedules the tick so live updates keep flowing.
func (d *mcpDashboard) refreshSnapshot() tea.Cmd {
	return tea.Batch(d.loadSnapshotCmd(), d.refreshTick())
}

// applySnapshot installs a freshly-loaded server list and re-clamps the cursor.
func (d *mcpDashboard) applySnapshot(msg mcpDashboardSnapshotMsg) tea.Cmd {
	if msg.err != nil {
		// Keep the last-known list; surface the error inline.
		d.loaded = true
		return d.setActionMessage("list failed: " + msg.err.Error())
	}
	d.servers = msg.servers
	d.loaded = true
	d.clampCursor()
	return nil
}

// applyActionMsg records the outcome of a reconnect/remove/add and refreshes.
func (d *mcpDashboard) applyActionMsg(msg mcpDashboardActionMsg) tea.Cmd {
	var text string
	if msg.err != nil {
		text = fmt.Sprintf("%s %s failed: %s", msg.verb, msg.name, msg.err.Error())
	} else {
		text = fmt.Sprintf("%s %s ✓", msg.verb, msg.name)
	}
	return tea.Batch(d.setActionMessage(text), d.loadSnapshotCmd())
}

func (d *mcpDashboard) clampCursor() {
	if d.cursor < 0 {
		d.cursor = 0
	}
	if d.cursor >= len(d.servers) {
		d.cursor = maxInt(0, len(d.servers)-1)
	}
}

// selectedServer returns the server under the cursor, or false if the list is
// empty.
func (d *mcpDashboard) selectedServer() (agentclient.McpServer, bool) {
	if d.cursor < 0 || d.cursor >= len(d.servers) {
		return agentclient.McpServer{}, false
	}
	return d.servers[d.cursor], true
}

// --- contentPage ----------------------------------------------------------

func (d *mcpDashboard) ID() contentPageID { return contentPageMcp }

// blurBody implements bodyFocusablePage: focus has lifted back to the config
// tab strip, so stop drawing the row cursor marker until the body is
// re-entered.
func (d *mcpDashboard) blurBody() { d.bodyFocused = false }

// stripForwardKeys implements stripForwardingPage: while the config tab strip
// owns the keyboard, these keys should still drop focus into the body and be
// forwarded here, so the action hotkeys advertised in the hint row (a/r/x)
// work on the first press instead of being swallowed by the strip.
func (d *mcpDashboard) stripForwardKeys() []string { return []string{"a", "r", "x", "d"} }

// wantsEscape implements escapeConsumingPage: while a details or add-server
// overlay is open, Esc should close that overlay (routed here via Update)
// rather than stepping focus back to the config tab strip.
func (d *mcpDashboard) wantsEscape() bool { return d.details != nil || d.popover != nil }

func (d *mcpDashboard) SetSize(w, h int) {
	d.width = w
	d.height = h
}

// handlePaste routes a bracketed paste into the add-server form when it is
// open. Returns true when the paste was consumed. The details overlay is
// read-only, so paste is ignored there and everywhere else on the dashboard.
func (d *mcpDashboard) handlePaste(text string) bool {
	if d.popover == nil {
		return false
	}
	return d.popover.paste(text)
}

// Update handles keys. When the popover is open, keys route to it first.
func (d *mcpDashboard) Update(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	// Reaching Update means the config surface has handed keyboard control to
	// the body, so the cursor marker may now be drawn.
	d.bodyFocused = true

	if d.details != nil {
		cmd, closed := d.details.Update(msg)
		if closed {
			d.details = nil
		}
		return cmd, false
	}

	if d.popover != nil {
		cmd, closed, submit := d.popover.Update(msg)
		if submit != nil {
			d.popover = nil
			return d.addServerCmd(*submit), false
		}
		if closed {
			d.popover = nil
		}
		return cmd, false
	}

	switch msg.String() {
	case "up", "k":
		if d.cursor > 0 {
			d.cursor--
		}
		return nil, false
	case "down", "j":
		if d.cursor < len(d.servers)-1 {
			d.cursor++
		}
		return nil, false
	case "a":
		d.popover = newMcpAddForm(d.palette, d.styles)
		return nil, false
	case "d", "enter":
		if s, ok := d.selectedServer(); ok {
			d.details = newMcpDetails(d.palette, d.styles, s)
		}
		return nil, false
	case "r":
		if s, ok := d.selectedServer(); ok {
			return tea.Batch(d.setActionMessage("reconnecting "+s.Name+"…"), d.reconnectCmd(s.Name)), false
		}
		return nil, false
	case "x":
		if s, ok := d.selectedServer(); ok {
			return tea.Batch(d.setActionMessage("removing "+s.Name+"…"), d.removeCmd(s.Name)), false
		}
		return nil, false
	}
	return nil, false
}

// --- async action commands ------------------------------------------------

func (d *mcpDashboard) reconnectCmd(name string) tea.Cmd {
	ag := d.agent
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := ag.RestartMcpServer(ctx, name)
		return mcpDashboardActionMsg{verb: "reconnect", name: name, err: err}
	}
}

func (d *mcpDashboard) removeCmd(name string) tea.Cmd {
	ag := d.agent
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := ag.RemoveMcpServer(ctx, name)
		return mcpDashboardActionMsg{verb: "remove", name: name, err: err}
	}
}

func (d *mcpDashboard) addServerCmd(sub mcpAddSubmit) tea.Cmd {
	ag := d.agent
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := ag.AddMcpServer(ctx, sub.name, sub.command, sub.args, sub.env)
		return mcpDashboardActionMsg{verb: "add", name: sub.name, err: err}
	}
}

// stateStyle maps an MCP server state to a themed style.
func (d *mcpDashboard) stateStyle(state string) lipgloss.Style {
	switch strings.ToLower(state) {
	case "ready":
		return d.styles.Success
	case "connecting":
		return d.styles.Warn
	case "failed", "error":
		return d.styles.Error
	default:
		return d.styles.Muted
	}
}

// --- scroller (thin: the row list is short) -------------------------------

func (d *mcpDashboard) ScrollBy(int) {}
func (d *mcpDashboard) ScrollTo(int) {}
func (d *mcpDashboard) ScrollState() contentPageScrollState {
	return contentPageScrollState{Total: len(d.servers), Height: d.height, Offset: 0}
}

// --- rendering ------------------------------------------------------------

func (d *mcpDashboard) View() string {
	base := d.renderList()

	// Float whichever overlay is open — details or the add form — centered over
	// the list. composeOverlay never adds rows: box lines that fall past the
	// end of base are dropped. renderList only produces as many rows as it has
	// content (a handful), far shorter than d.height, so the centered overlay
	// would land on rows that don't exist and vanish. Pad base up to d.height
	// first so every overlay row has a line to splice onto.
	var overlay string
	switch {
	case d.details != nil:
		overlay = d.details.View()
	case d.popover != nil:
		overlay = d.popover.View()
	default:
		return base
	}
	fw := lipgloss.Width(overlay)
	fh := lipgloss.Height(overlay)
	x := maxInt(0, (d.width-fw)/2)
	y := maxInt(0, (d.height-fh)/2)
	base = padViewHeight(base, d.height)
	return composeOverlay(base, overlay, x, y)
}

// padViewHeight appends blank lines to s until it has at least h lines, so a
// centered overlay composited onto it has rows to land on across the full
// frame height.
func padViewHeight(s string, h int) string {
	n := strings.Count(s, "\n") + 1
	if n >= h {
		return s
	}
	return s + strings.Repeat("\n", h-n)
}

func (d *mcpDashboard) renderList() string {
	totalW := maxInt(20, d.width)
	// Inner width available inside the rounded-border block (border + padding).
	contentW := dashboardBlockContentWidth(totalW)

	var lines []string

	switch {
	case !d.loaded:
		lines = append(lines, d.styles.Muted.Render("loading…"))
	case len(d.servers) == 0:
		lines = append(lines,
			d.styles.Muted.Render("no MCP servers configured"),
			"",
			d.styles.Dim.Render("press ")+
				d.styles.Accent.Render("a")+
				d.styles.Dim.Render(" to add one"),
		)
	default:
		// Column layout: marker + name | state | tools | error.
		nameW := clampInt(contentW*30/100, 12, 28)
		stateW := 12
		toolsW := 8
		errW := maxInt(0, contentW-2-nameW-stateW-toolsW-9)

		for i, s := range d.servers {
			selected := d.bodyFocused && i == d.cursor
			marker := "  "
			nameStyle := d.styles.Primary
			if selected {
				marker = d.styles.SelectionCaret.Render("▶ ")
				nameStyle = d.styles.Bright
			}
			name := nameStyle.Render(padRightPlain(truncatePlain(s.Name, nameW), nameW))
			state := d.stateStyle(s.State).Render(padRightPlain(truncatePlain(s.State, stateW), stateW))
			tools := d.styles.Muted.Render(padRightPlain(fmt.Sprintf("%d tools", s.ToolCount), toolsW))
			line := marker + name +
				d.styles.BorderDim.Render(" │ ") + state +
				d.styles.BorderDim.Render(" │ ") + tools
			if errW > 0 && s.Err != "" {
				line += d.styles.BorderDim.Render(" │ ") +
					d.styles.Error.Render(truncatePlain(s.Err, errW))
			}
			lines = append(lines, line)
		}
	}

	lines = append(lines, "")
	if d.actionMessage != "" {
		lines = append(lines, d.styles.Muted.Render(truncatePlain(d.actionMessage, contentW)))
	}
	hint := d.styles.Accent.Render("a") + d.styles.Dim.Render(" add · ") +
		d.styles.Accent.Render("d") + d.styles.Dim.Render(" details · ") +
		d.styles.Accent.Render("r") + d.styles.Dim.Render(" reconnect · ") +
		d.styles.Accent.Render("x") + d.styles.Dim.Render(" remove")
	lines = append(lines, hint)

	return renderRuntimeDashboardTextBlock("Hosted MCP servers", lines, totalW, 0, d.palette, d.styles)
}
