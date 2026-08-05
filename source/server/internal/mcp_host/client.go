// source/server/internal/mcp_host/client.go
package mcphost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"cercano/source/server/internal/llm"
)

// remoteTool is a tool as advertised by an external MCP server.
type remoteTool struct {
	Name        string
	Description string
	Schema      json.RawMessage
	Destructive bool
}

// conn is one live MCP client session over a transport.
type conn struct {
	sess *mcp.ClientSession
}

// dial connects an MCP client over the given transport and completes the
// initialize handshake.
func dial(ctx context.Context, t mcp.Transport) (*conn, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "cercano", Version: "0.1.0"}, nil)
	sess, err := client.Connect(ctx, t, nil)
	if err != nil {
		return nil, err
	}
	return &conn{sess: sess}, nil
}

// listTools enumerates the server's advertised tools, marshaling each input
// schema back to raw JSON for the agent's tool catalog.
func (c *conn) listTools(ctx context.Context) ([]remoteTool, error) {
	res, err := c.sess.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]remoteTool, 0, len(res.Tools))
	for _, t := range res.Tools {
		schema, err := json.Marshal(t.InputSchema)
		if err != nil {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		destructive := false
		if t.Annotations != nil && t.Annotations.DestructiveHint != nil {
			destructive = *t.Annotations.DestructiveHint
		}
		out = append(out, remoteTool{
			Name:        t.Name,
			Description: t.Description,
			Schema:      schema,
			Destructive: destructive,
		})
	}
	return out, nil
}

// call invokes a tool. Returns (text, images, isToolError, transportError). A
// tool-level error (isToolError) carries its message in text; a transport error
// means the call never completed. images holds any image parts of the result as
// BlockImage-typed llm.Blocks.
func (c *conn) call(ctx context.Context, tool string, args json.RawMessage) (string, []llm.Block, bool, error) {
	var arguments any
	if len(args) > 0 {
		arguments = args
	}
	res, err := c.sess.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: arguments})
	if err != nil {
		return "", nil, false, err
	}
	text, images := flattenContent(res.Content)
	return text, images, res.IsError, nil
}

func (c *conn) close() error {
	if c.sess == nil {
		return nil
	}
	return c.sess.Close()
}

// flattenContent splits an MCP tool result into its concatenated text parts and
// its image parts. Image parts become BlockImage-typed llm.Blocks with MediaType
// + base64 ImageData; the tool loop decides (per model vision support) whether to
// forward them. Resource/embedded content is still ignored.
func flattenContent(content []mcp.Content) (string, []llm.Block) {
	var b strings.Builder
	var images []llm.Block
	for _, part := range content {
		switch p := part.(type) {
		case *mcp.TextContent:
			b.WriteString(p.Text)
		case *mcp.ImageContent:
			images = append(images, llm.Block{
				Type:      llm.BlockImage,
				MediaType: p.MIMEType,
				ImageData: base64.StdEncoding.EncodeToString(p.Data),
			})
		}
	}
	return b.String(), images
}

var errNoSession = errors.New("mcp: no live session")
