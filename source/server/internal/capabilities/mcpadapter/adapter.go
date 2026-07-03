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
// Synonyms are additional names the capability is discoverable under; each
// synonym registers an extra cercano_<synonym> tool that routes back to the
// same canonical capability via InvokeCapability.
type CapMeta struct {
	Name        string
	Description string
	Schema      json.RawMessage
	Synonyms    []string
}

// ToolName returns the MCP tool name for a capability's canonical name.
func ToolName(m CapMeta) string { return "cercano_" + m.Name }

// synonymToolName returns the MCP tool name for one of a capability's synonyms.
func synonymToolName(syn string) string { return "cercano_" + syn }

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
// Every synonym on a CapMeta is registered as an additional tool whose handler
// routes to the same canonical capability, so callers reaching for either name
// find the tool.
func RegisterCapabilities(srv *gomcp.Server, client proto.AgentClient, caps []CapMeta) {
	for _, m := range caps {
		m := m
		handler := makeHandler(client, m.Name)
		srv.AddTool(&gomcp.Tool{
			Name:        ToolName(m),
			Description: m.Description,
			InputSchema: parseSchema(m.Schema),
		}, handler)
		primary := ToolName(m)
		for _, syn := range m.Synonyms {
			if syn == "" {
				continue
			}
			name := synonymToolName(syn)
			if name == primary {
				continue
			}
			srv.AddTool(&gomcp.Tool{
				Name:        name,
				Description: m.Description,
				InputSchema: parseSchema(m.Schema),
			}, handler)
		}
	}
}

// makeHandler builds a tool handler that forwards to InvokeCapability using
// the given canonical capability name, regardless of which MCP tool name the
// caller invoked (canonical or synonym).
func makeHandler(client proto.AgentClient, canonical string) gomcp.ToolHandler {
	return gomcp.ToolHandler(func(ctx context.Context, req *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
		var raw json.RawMessage
		if req.Params != nil {
			raw = req.Params.Arguments
		}
		resp, err := client.InvokeCapability(ctx, &proto.InvokeCapabilityRequest{
			Name:     canonical,
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
}
