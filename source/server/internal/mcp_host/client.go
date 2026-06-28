// source/server/internal/mcp_host/client.go
package mcphost

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// call invokes a tool. Returns (text, isToolError, transportError). A tool-level
// error (isToolError) carries its message in text; a transport error means the
// call never completed.
func (c *conn) call(ctx context.Context, tool string, args json.RawMessage) (string, bool, error) {
	var arguments any
	if len(args) > 0 {
		arguments = args
	}
	res, err := c.sess.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: arguments})
	if err != nil {
		return "", false, err
	}
	return flattenContent(res.Content), res.IsError, nil
}

func (c *conn) close() error {
	if c.sess == nil {
		return nil
	}
	return c.sess.Close()
}

// flattenContent concatenates the text parts of an MCP tool result. Non-text
// content (images, resources) is ignored in v1.
func flattenContent(content []mcp.Content) string {
	var b strings.Builder
	for _, part := range content {
		if tc, ok := part.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

var errNoSession = errors.New("mcp: no live session")
