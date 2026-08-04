package ui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// mcpDetails is a read-only popover showing one hosted MCP server's full
// launch config (command / args / env) alongside its live status. It exists so
// the config-tab list can double as a reference for reconstructing a server —
// e.g. reading off the exact command to re-add one after removing it.
//
// The TUI runs in the alternate screen with mouse reporting on, so a user
// cannot drag-select and ⌘C the command out of the terminal. Instead the
// command line is wrapped so it is fully readable, and `c` copies it to the
// system clipboard via the same OSC 52 + pbcopy path chat-view copy uses. Any
// other key closes the popover.
type mcpDetails struct {
	palette theme.Palette
	styles  theme.Styles
	server  agentclient.McpServer
	copied  bool // set once `c` has copied the command this session
}

func newMcpDetails(p theme.Palette, s theme.Styles, srv agentclient.McpServer) *mcpDetails {
	return &mcpDetails{palette: p, styles: s, server: srv}
}

// Update handles a key. `c` copies the command line to the clipboard and keeps
// the popover open (returning the clipboard cmd); any other key closes it.
func (d *mcpDetails) Update(msg tea.KeyPressMsg) (cmd tea.Cmd, closed bool) {
	switch msg.String() {
	case "c":
		d.copied = true
		return selectionClipboardCmd(d.commandLine()), false
	default:
		return nil, true
	}
}

// commandLine renders the full command + args on a single line — the exact
// string a user would paste to re-add the server.
func (d *mcpDetails) commandLine() string {
	parts := append([]string{d.server.Command}, d.server.Args...)
	return strings.Join(parts, " ")
}

func (d *mcpDetails) View() string {
	const boxW = 60
	fieldW := boxW - 4

	var b strings.Builder
	b.WriteString(d.styles.Bright.Render(truncatePlain(d.server.Name, fieldW)))
	b.WriteString("\n\n")

	row := func(label, value string) {
		b.WriteString(d.styles.Muted.Render(padRightPlain(label, 9)))
		b.WriteString(" ")
		b.WriteString(d.styles.Primary.Render(truncatePlain(value, fieldW-10)))
		b.WriteString("\n")
	}

	// State gets its own colored render rather than the plain row helper.
	b.WriteString(d.styles.Muted.Render(padRightPlain("state", 9)))
	b.WriteString(" ")
	b.WriteString(d.stateStyle(d.server.State).Render(truncatePlain(d.server.State, fieldW-10)))
	b.WriteString("\n")

	row("tools", fmt.Sprintf("%d", d.server.ToolCount))
	if d.server.Err != "" {
		b.WriteString(d.styles.Muted.Render(padRightPlain("error", 9)))
		b.WriteString(" ")
		b.WriteString(d.styles.Error.Render(truncatePlain(d.server.Err, fieldW-10)))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	// Command line on its own full-width block, wrapped so the whole path is
	// readable. ansi.Wrap hard-breaks the long unbroken path token.
	b.WriteString(d.styles.Muted.Render("command"))
	b.WriteString("\n")
	b.WriteString(d.styles.Primary.Render(ansi.Wrap(d.commandLine(), fieldW, "")))
	b.WriteString("\n")

	if len(d.server.Env) > 0 {
		b.WriteString("\n")
		b.WriteString(d.styles.Muted.Render("env"))
		b.WriteString("\n")
		// Sort keys so the display is stable across polls.
		keys := make([]string, 0, len(d.server.Env))
		for k := range d.server.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			line := k + "=" + d.server.Env[k]
			b.WriteString(d.styles.Primary.Render(ansi.Wrap(line, fieldW, "")))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	copyLabel := "copy command"
	if d.copied {
		copyLabel = "command copied ✓"
	}
	b.WriteString(d.styles.Accent.Render("c") + d.styles.Dim.Render(" "+copyLabel))
	b.WriteString(d.styles.Dim.Render(" · "))
	b.WriteString(d.styles.Accent.Render("esc") + d.styles.Dim.Render(" close"))
	b.WriteString("\n")
	// The TUI captures the mouse, so ordinary drag-select is off. Point at the
	// terminal's own bypass (Option-drag on macOS terminals) for native select.
	b.WriteString(d.styles.Dim.Render("⌥-drag to select text"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(d.palette.Border).
		Padding(0, 1).
		Width(boxW).
		Render(b.String())
	return box
}

// stateStyle mirrors mcpDashboard.stateStyle so the details view colors state
// consistently with the list.
func (d *mcpDetails) stateStyle(state string) lipgloss.Style {
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
