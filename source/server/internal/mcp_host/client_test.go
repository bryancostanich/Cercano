// source/server/internal/mcp_host/client_test.go
package mcphost

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoIn struct {
	Text string `json:"text" jsonschema:"the text to echo"`
}

func startTestServer(t *testing.T, ctx context.Context) mcp.Transport {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "echo", Description: "echoes text"},
		func(ctx context.Context, req *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "echo:" + in.Text}},
			}, nil, nil
		})
	serverT, clientT := mcp.NewInMemoryTransports()
	go func() { _ = srv.Run(ctx, serverT) }()
	return clientT
}

func TestConnListCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientT := startTestServer(t, ctx)

	c, err := dial(ctx, clientT)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()

	tools, err := c.listTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}

	text, isErr, err := c.call(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	if text != "echo:hi" {
		t.Fatalf("call result = %q", text)
	}
}
