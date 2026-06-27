package mcphost

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"cercano/source/server/internal/agenttools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	if !errors.Is(err, errors.New("warming")) && err.Error() == "" {
		t.Fatalf("error message empty")
	}
	// underlying cause must be wrapped
	if err.Error() == "mcp server \"github\" unavailable — /mcp restart github" {
		t.Fatalf("underlying error not wrapped: %v", err)
	}
}

func TestMCPToolExecuteToolError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Stand up a server with a tool that returns IsError:true
	srv := mcp.NewServer(&mcp.Implementation{Name: "errsrv", Version: "0.0.1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "fail", Description: "always fails"},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "boom"}},
			}, nil, nil
		})
	serverT, clientT := mcp.NewInMemoryTransports()
	go func() { _ = srv.Run(ctx, serverT) }()

	c, err := dial(ctx, clientT)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()

	ready := func(ctx context.Context) (*conn, error) { return c, nil }
	tl := newMCPTool("errsrv", remoteTool{Name: "fail"}, ready)

	_, execErr := tl.Execute(ctx, json.RawMessage(`{}`))
	if execErr == nil {
		t.Fatal("want error when tool returns IsError:true")
	}
	if execErr.Error() != "boom" {
		t.Fatalf("error message = %q, want \"boom\"", execErr.Error())
	}
}
