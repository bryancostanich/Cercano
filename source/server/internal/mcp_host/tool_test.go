package mcphost

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"cercano/source/server/internal/agenttools"
)

func TestMCPToolMetadata(t *testing.T) {
	rt := remoteTool{Name: "create_issue", Description: "make an issue",
		Schema: json.RawMessage(`{"type":"object"}`)}
	tl := newMCPTool("github", rt, nil)

	if tl.Name() != "mcp__github__create_issue" {
		t.Fatalf("name = %q", tl.Name())
	}
	if tl.Permission() != agenttools.PermW {
		t.Fatalf("permission = %q, want W", tl.Permission())
	}
	if agenttools.OriginOf(tl) != agenttools.OriginMCP {
		t.Fatalf("origin not mcp")
	}
	if string(tl.Schema()) != `{"type":"object"}` {
		t.Fatalf("schema = %s", tl.Schema())
	}
}

func TestMCPToolExecuteProxies(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientT := startTestServer(t, ctx) // from client_test.go
	c, err := dial(ctx, clientT)
	if err != nil {
		t.Fatal(err)
	}
	ready := func(ctx context.Context) (*conn, error) { return c, nil }

	tl := newMCPTool("test", remoteTool{Name: "echo"}, ready)
	res, err := tl.Execute(ctx, json.RawMessage(`{"text":"yo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "echo:yo" {
		t.Fatalf("text = %q", res.Text)
	}
}

func TestMCPToolExecuteUnavailable(t *testing.T) {
	ready := func(ctx context.Context) (*conn, error) {
		return nil, errors.New("warming")
	}
	tl := newMCPTool("github", remoteTool{Name: "x"}, ready)
	_, err := tl.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("want error when server unavailable")
	}
}
