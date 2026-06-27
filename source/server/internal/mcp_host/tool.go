package mcphost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"cercano/source/server/internal/agenttools"
)

// readyFunc resolves a live connection to the tool's server, blocking until the
// server is ready or returning an error if it is unavailable.
type readyFunc func(ctx context.Context) (*conn, error)

// mcpTool adapts one external MCP tool to the agent's Tool interface. Every MCP
// tool is PermW so it routes through the permission gate (R-tier tools bypass
// the gate entirely); the gate then forces a confirm-by-default for MCP origin
// unless allowlisted.
type mcpTool struct {
	server      string
	tool        string
	fqName      string
	desc        string
	schema      json.RawMessage
	destructive bool
	ready       readyFunc
}

func newMCPTool(server string, rt remoteTool, ready readyFunc) *mcpTool {
	schema := rt.Schema
	if len(schema) == 0 {
		schema = json.RawMessage(`{"type":"object"}`)
	}
	return &mcpTool{
		server:      server,
		tool:        rt.Name,
		fqName:      ToolName(server, rt.Name),
		desc:        rt.Description,
		schema:      schema,
		destructive: rt.Destructive,
		ready:       ready,
	}
}

func (t *mcpTool) Name() string                     { return t.fqName }
func (t *mcpTool) Description() string              { return t.desc }
func (t *mcpTool) Permission() agenttools.Permission { return agenttools.PermW }
func (t *mcpTool) Origin() agenttools.Origin        { return agenttools.OriginMCP }
func (t *mcpTool) Schema() json.RawMessage          { return t.schema }

func (t *mcpTool) Execute(ctx context.Context, raw json.RawMessage) (*agenttools.Result, error) {
	c, err := t.ready(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp server %q unavailable — /mcp restart %s", t.server, t.server)
	}
	text, isToolErr, callErr := c.call(ctx, t.tool, raw)
	if callErr != nil {
		return nil, fmt.Errorf("mcp %s: %w", t.fqName, callErr)
	}
	if isToolErr {
		if text == "" {
			text = "tool reported an error"
		}
		return nil, errors.New(text)
	}
	res := agenttools.NewTextResult(text)
	res.Detail = "mcp"
	return res, nil
}
