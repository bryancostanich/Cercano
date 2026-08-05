// source/server/internal/mcp_host/client_test.go
package mcphost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"cercano/source/server/internal/llm"
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

	text, images, isErr, err := c.call(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	if text != "echo:hi" {
		t.Fatalf("call result = %q", text)
	}
	if len(images) != 0 {
		t.Fatalf("expected no images, got %d", len(images))
	}
}

func TestFlattenContent_TextAndImage(t *testing.T) {
	raw := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	content := []mcp.Content{
		&mcp.TextContent{Text: "hello "},
		&mcp.ImageContent{MIMEType: "image/png", Data: raw},
		&mcp.TextContent{Text: "world"},
	}
	text, images := flattenContent(content)
	if text != "hello world" {
		t.Fatalf("text = %q, want %q", text, "hello world")
	}
	if len(images) != 1 {
		t.Fatalf("images = %d, want 1", len(images))
	}
	img := images[0]
	if img.Type != llm.BlockImage {
		t.Fatalf("image block type = %q, want %q", img.Type, llm.BlockImage)
	}
	if img.MediaType != "image/png" {
		t.Fatalf("media type = %q", img.MediaType)
	}
	if want := base64.StdEncoding.EncodeToString(raw); img.ImageData != want {
		t.Fatalf("image data = %q, want %q", img.ImageData, want)
	}
}
