package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func newTestMcpDashboard(servers []agentclient.McpServer) *mcpDashboard {
	d := &mcpDashboard{
		palette: theme.Palette{},
		styles:  theme.Styles{},
		width:   80,
		height:  24,
		loaded:  true,
		servers: servers,
	}
	return d
}

func TestMcpDashboard_CursorNavClamped(t *testing.T) {
	d := newTestMcpDashboard([]agentclient.McpServer{
		{Name: "a", State: "ready"},
		{Name: "b", State: "connecting"},
	})
	// Up at top stays at 0.
	d.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if d.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", d.cursor)
	}
	d.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if d.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", d.cursor)
	}
	// Down at bottom stays at last.
	d.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if d.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 (clamped)", d.cursor)
	}
}

func TestMcpDashboard_EmptyState(t *testing.T) {
	d := newTestMcpDashboard(nil)
	out := d.View()
	if !strings.Contains(out, "no MCP servers") {
		t.Fatalf("empty state missing hint:\n%s", out)
	}
}

func TestMcpDashboard_RowRender(t *testing.T) {
	d := newTestMcpDashboard([]agentclient.McpServer{
		{Name: "viz", State: "ready", ToolCount: 22},
	})
	out := d.View()
	for _, want := range []string{"viz", "ready", "22 tools"} {
		if !strings.Contains(out, want) {
			t.Fatalf("row missing %q:\n%s", want, out)
		}
	}
}

func TestMcpDashboard_CaretHiddenUntilBodyFocused(t *testing.T) {
	d := newTestMcpDashboard([]agentclient.McpServer{
		{Name: "a", State: "ready"},
	})
	// Fresh dashboard: strip still owns focus, so no cursor caret is drawn.
	if got := d.View(); strings.Contains(got, "▶") {
		t.Fatalf("caret drawn before body focus:\n%s", got)
	}
	// Any delegated key means focus has entered the body; the caret appears.
	d.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := d.View(); !strings.Contains(got, "▶") {
		t.Fatalf("caret missing after body focus:\n%s", got)
	}
	// Lifting focus back to the strip hides the caret again.
	d.blurBody()
	if got := d.View(); strings.Contains(got, "▶") {
		t.Fatalf("caret still drawn after blurBody:\n%s", got)
	}
}

func TestMcpDashboard_ReconnectEmitsCmdForSelected(t *testing.T) {
	d := newTestMcpDashboard([]agentclient.McpServer{
		{Name: "a", State: "ready"},
		{Name: "b", State: "failed"},
	})
	d.cursor = 1
	cmd, _ := d.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if cmd == nil {
		t.Fatal("reconnect produced no cmd")
	}
	if !strings.Contains(d.actionMessage, "b") {
		t.Fatalf("action message = %q, want to mention b", d.actionMessage)
	}
}

func TestMcpDashboard_RemoveEmitsCmdForSelected(t *testing.T) {
	d := newTestMcpDashboard([]agentclient.McpServer{{Name: "a", State: "ready"}})
	cmd, _ := d.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if cmd == nil {
		t.Fatal("remove produced no cmd")
	}
}

func TestMcpDashboard_AKeyOpensPopover(t *testing.T) {
	d := newTestMcpDashboard(nil)
	d.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if d.popover == nil {
		t.Fatal("a did not open the popover")
	}
	// Esc closes it.
	d.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if d.popover != nil {
		t.Fatal("esc did not close the popover")
	}
}

// TestMcpDashboard_PopoverVisibleInView guards the render path, not just the
// state flag: opening the add form must actually paint its fields into the
// composited View. The overlay is centered against d.height, but renderList
// only produces a few content rows, so without padding the base to the frame
// height composeOverlay drops every form row past the list's end and the
// popover vanishes on screen even though d.popover is non-nil.
func TestMcpDashboard_PopoverVisibleInView(t *testing.T) {
	d := newTestMcpDashboard([]agentclient.McpServer{{Name: "a", State: "ready"}})
	d.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if d.popover == nil {
		t.Fatal("popover did not open")
	}
	out := d.View()
	for _, want := range []string{"Add MCP server", "command", "env"} {
		if !strings.Contains(out, want) {
			t.Fatalf("popover text %q missing from composited View:\n%s", want, out)
		}
	}
}

func TestMcpDashboard_ActionMessageAutoClears(t *testing.T) {
	d := newTestMcpDashboard([]agentclient.McpServer{{Name: "a", State: "ready"}})

	// Setting a message returns a clear cmd and bumps the generation.
	cmd := d.setActionMessage("reconnect a ✓")
	if cmd == nil {
		t.Fatal("setActionMessage returned no clear cmd")
	}
	if d.actionMessage == "" {
		t.Fatal("action message not set")
	}
	gen := d.actionMsgGen

	// A stale clear (older generation) is ignored.
	d.clearActionMessage(gen - 1)
	if d.actionMessage == "" {
		t.Fatal("stale clear wiped the current message")
	}

	// The matching clear drops it.
	d.clearActionMessage(gen)
	if d.actionMessage != "" {
		t.Fatalf("matching clear left message = %q", d.actionMessage)
	}
}

func TestMcpDashboard_NewMessageSupersedesPendingClear(t *testing.T) {
	d := newTestMcpDashboard([]agentclient.McpServer{{Name: "a", State: "ready"}})
	d.setActionMessage("first")
	firstGen := d.actionMsgGen
	d.setActionMessage("second")

	// The first message's scheduled clear must not wipe the second.
	d.clearActionMessage(firstGen)
	if d.actionMessage != "second" {
		t.Fatalf("stale clear wiped superseding message; got %q", d.actionMessage)
	}
}

func TestMcpDashboard_DKeyOpensDetailsForSelected(t *testing.T) {
	d := newTestMcpDashboard([]agentclient.McpServer{
		{Name: "alpha", State: "ready"},
		{Name: "beta", State: "ready"},
	})
	d.bodyFocused = true
	d.cursor = 1
	d.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if d.details == nil {
		t.Fatal("d did not open the details popover")
	}
	if d.details.server.Name != "beta" {
		t.Fatalf("details server = %q, want the selected row \"beta\"", d.details.server.Name)
	}
	// Any key closes it.
	d.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if d.details != nil {
		t.Fatal("esc did not close the details popover")
	}
}

// TestMcpDashboard_DetailsVisibleInView guards the render path: opening details
// must paint the server's launch config into the composited View, not just set
// the flag. Same padding hazard as the add-form popover.
func TestMcpDashboard_DetailsVisibleInView(t *testing.T) {
	d := newTestMcpDashboard([]agentclient.McpServer{{
		Name:    "rekolektion-viz",
		State:   "ready",
		Command: "dotnet",
		Args:    []string{"run", "--project", "/x/y"},
		Env:     map[string]string{"FOO": "bar"},
	}})
	d.bodyFocused = true
	d.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if d.details == nil {
		t.Fatal("details did not open")
	}
	out := d.View()
	for _, want := range []string{"rekolektion-viz", "dotnet run --project /x/y", "FOO=bar", "esc"} {
		if !strings.Contains(out, want) {
			t.Fatalf("details text %q missing from composited View:\n%s", want, out)
		}
	}
}

// TestMcpDashboard_DetailsCopyKeepsOpen verifies the copy escape hatch: because
// the TUI captures the mouse, drag-select copy is impossible, so `c` must copy
// the command to the clipboard (non-nil cmd) and keep the popover open — only a
// non-`c` key closes it.
func TestMcpDashboard_DetailsCopyKeepsOpen(t *testing.T) {
	d := newTestMcpDashboard([]agentclient.McpServer{{
		Name:    "rekolektion-viz",
		State:   "ready",
		Command: "dotnet",
		Args:    []string{"run", "--project", "/x/y"},
	}})
	d.bodyFocused = true
	d.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if d.details == nil {
		t.Fatal("details did not open")
	}
	cmd, _ := d.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if cmd == nil {
		t.Fatal("c did not return a clipboard command")
	}
	if d.details == nil {
		t.Fatal("c must keep the details popover open, not close it")
	}
	if !d.details.copied {
		t.Fatal("c did not flag the command as copied")
	}
	// The copied acknowledgement should now render.
	if !strings.Contains(d.View(), "copied") {
		t.Fatalf("View missing copied ack:\n%s", d.View())
	}
	// A non-c key still closes.
	d.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if d.details != nil {
		t.Fatal("esc did not close details after copy")
	}
}

// TestMcpDashboard_DetailsWrapsLongCommand guards that a long command path is
// rendered in full (wrapped across rows), not truncated with an ellipsis. We
// assert every path segment survives into the View.
func TestMcpDashboard_DetailsWrapsLongCommand(t *testing.T) {
	d := newTestMcpDashboard([]agentclient.McpServer{{
		Name:    "rekolektion-viz",
		State:   "ready",
		Command: "/Users/bryancostanich/.dotnet/dotnet",
		Args:    []string{"run", "--project", "/Users/bryancostanich/git_repos/rekolektion/src/mcp"},
	}})
	d.bodyFocused = true
	d.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	out := stripAnsiCSI(d.View())
	for _, want := range []string{".dotnet/dotnet", "--project", "rekolektion/src/mcp"} {
		if !strings.Contains(out, want) {
			t.Fatalf("wrapped command missing %q from View:\n%s", want, out)
		}
	}
	if strings.Contains(out, "…") || strings.Contains(out, "...") {
		t.Fatalf("command was truncated instead of wrapped:\n%s", out)
	}
}

func TestMcpDashboard_SnapshotReplacesAndClamps(t *testing.T) {
	d := newTestMcpDashboard([]agentclient.McpServer{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	})
	d.cursor = 2
	d.applySnapshot(mcpDashboardSnapshotMsg{servers: []agentclient.McpServer{{Name: "a"}}})
	if len(d.servers) != 1 {
		t.Fatalf("servers = %d, want 1", len(d.servers))
	}
	if d.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (re-clamped)", d.cursor)
	}
}
