package server

import (
	"context"
	"testing"

	mcphost "cercano/source/server/internal/mcp_host"
	"cercano/source/server/pkg/proto"
)

type fakeMgr struct {
	added   string
	removed string
}

func (f *fakeMgr) List() []mcphost.ServerStatus {
	return []mcphost.ServerStatus{{Name: "github", State: mcphost.StateReady, ToolCount: 3}}
}
func (f *fakeMgr) Add(_ context.Context, name string, _ mcphost.ServerConfig) error {
	f.added = name
	return nil
}
func (f *fakeMgr) Remove(_ context.Context, name string) error { f.removed = name; return nil }
func (f *fakeMgr) Restart(_ context.Context, _ string) error   { return nil }

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
