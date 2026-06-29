// Package mcpadapter exposes capabilities on the MCP plugin surface. Each
// capability becomes a cercano_<name> tool whose handler forwards execution to
// the agent's InvokeCapability RPC.
package mcpadapter

import (
	"context"
	"encoding/json"

	jsonschema "github.com/google/jsonschema-go/jsonschema"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"cercano/source/server/pkg/proto"
)

// CapMeta is the metadata the MCP surface needs to advertise a capability.
type CapMeta struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// ToolName returns the MCP tool name for a capability.
func ToolName(m CapMeta) string { return "cercano_" + m.Name }

// parseSchema converts a capability's raw JSON Schema into the *jsonschema.Schema
// the MCP SDK requires. Falls back to a generic {type:"object"} on any error.
func parseSchema(raw json.RawMessage) *jsonschema.Schema {
	if len(raw) == 0 {
		return &jsonschema.Schema{Type: "object"}
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil || s.Type != "object" {
		return &jsonschema.Schema{Type: "object"}
	}
	return &s
}

// RegisterCapabilities advertises each capability as an MCP tool that forwards
// to InvokeCapability over the gRPC client. Uses the low-level srv.AddTool so
// the handler receives raw JSON arguments without an extra unmarshal round-trip.
func RegisterCapabilities(srv *gomcp.Server, client proto.AgentClient, caps []CapMeta) {
	for _, m := range caps {
		m := m
		tool := &gomcp.Tool{
			Name:        ToolName(m),
			Description: m.Description,
			InputSchema: parseSchema(m.Schema),
		}
		handler := gomcp.ToolHandler(func(ctx context.Context, req *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
			var raw json.RawMessage
			if req.Params != nil {
				raw = req.Params.Arguments
			}
			resp, err := client.InvokeCapability(ctx, &proto.InvokeCapabilityRequest{
				Name:     m.Name,
				ArgsJson: raw,
			})
			if err != nil {
				return nil, err
			}
			if resp.IsError {
				return &gomcp.CallToolResult{
					IsError: true,
					Content: []gomcp.Content{&gomcp.TextContent{Text: resp.Error}},
				}, nil
			}
			return &gomcp.CallToolResult{
				Content: []gomcp.Content{&gomcp.TextContent{Text: string(resp.ResultJson)}},
			}, nil
		})
		srv.AddTool(tool, handler)
	}
}
