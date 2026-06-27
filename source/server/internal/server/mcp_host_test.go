package server

import (
	"context"
	"testing"

	mcphost "cercano/source/server/internal/mcp_host"
	"cercano/source/server/pkg/proto"
)

type fakeMgr struct {
	added     string
	removed   string
	restarted string
}

func (f *fakeMgr) List() []mcphost.ServerStatus {
	return []mcphost.ServerStatus{{Name: "github", State: mcphost.StateReady, ToolCount: 3}}
}
func (f *fakeMgr) Add(_ context.Context, name string, _ mcphost.ServerConfig) error {
	f.added = name
	return nil
}
func (f *fakeMgr) Remove(_ context.Context, name string) error  { f.removed = name; return nil }
func (f *fakeMgr) Restart(_ context.Context, name string) error { f.restarted = name; return nil }

func TestListAndAddMcpServers(t *testing.T) {
	s := &Server{}
	s.SetMcpManager(&fakeMgr{})

	resp, err := s.ListMcpServers(context.Background(), &proto.ListMcpServersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Servers) != 1 || resp.Servers[0].Name != "github" || resp.Servers[0].State != "ready" {
		t.Fatalf("list = %+v", resp.Servers)
	}

	fm := &fakeMgr{}
	s.SetMcpManager(fm)
	if _, err := s.AddMcpServer(context.Background(), &proto.AddMcpServerRequest{
		Name: "fs", Command: "mcp-fs", Args: []string{"/tmp"},
	}); err != nil {
		t.Fatal(err)
	}
	if fm.added != "fs" {
		t.Fatalf("Add not called with fs: %q", fm.added)
	}
}

func TestRemoveAndRestartMcpServers(t *testing.T) {
	fm := &fakeMgr{}
	s := &Server{}
	s.SetMcpManager(fm)

	if _, err := s.RemoveMcpServer(context.Background(), &proto.RemoveMcpServerRequest{Name: "github"}); err != nil {
		t.Fatal(err)
	}
	if fm.removed != "github" {
		t.Fatalf("Remove not called with github: %q", fm.removed)
	}

	resp, err := s.RestartMcpServer(context.Background(), &proto.RestartMcpServerRequest{Name: "github"})
	if err != nil {
		t.Fatal(err)
	}
	if fm.restarted != "github" {
		t.Fatalf("Restart not called with github: %q", fm.restarted)
	}
	// tool_count is populated from the post-restart List() snapshot (github → 3).
	if resp.GetToolCount() != 3 {
		t.Fatalf("restart tool_count = %d, want 3", resp.GetToolCount())
	}
}

func TestMcpHandlersNilManager(t *testing.T) {
	s := &Server{} // no manager set
	if resp, _ := s.ListMcpServers(context.Background(), &proto.ListMcpServersRequest{}); len(resp.GetServers()) != 0 {
		t.Fatal("nil manager should list no servers")
	}
	if resp, _ := s.AddMcpServer(context.Background(), &proto.AddMcpServerRequest{Name: "x"}); resp.GetOk() {
		t.Fatal("nil manager Add should not be ok")
	}
	if resp, _ := s.RestartMcpServer(context.Background(), &proto.RestartMcpServerRequest{Name: "x"}); resp.GetOk() {
		t.Fatal("nil manager Restart should not be ok")
	}
}
