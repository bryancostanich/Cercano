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
