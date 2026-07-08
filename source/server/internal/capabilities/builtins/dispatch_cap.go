package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/pkg/config"
)

type dispatchCap struct{}

// Dispatch constructs the dispatch capability.
func Dispatch() capabilities.Capability { return dispatchCap{} }

func (dispatchCap) Name() string            { return "dispatch" }
func (dispatchCap) Tier() capabilities.Tier { return capabilities.TierW }
func (dispatchCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (dispatchCap) Description() string {
	return "Run a sub-agent: hand off an open-ended task to a bounded tool-use loop over a granted set of tools (default: read-only tools). Returns the sub-agent's final result. Tool names passed in `tools` must be the plain registered names (e.g. \"Read\", \"Glob\") — do NOT include any host/MCP prefix like \"mcp__oc__\"."
}
func (dispatchCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["task"],
		"properties": {
			"task":            {"type": "string", "description": "Open-ended instruction for the sub-agent tool loop."},
			"tools":           {"type": "array", "items": {"type": "string"}, "description": "Tool or capability names to grant, using the plain registered names (e.g. \"Read\", \"Glob\", \"Grep\", \"Bash\") — no host or MCP prefix. Omit to default to read-only tools."},
			"conversation_id": {"type": "string", "description": "Optional conversation ID to associate with this dispatch."}
		}
	}`)
}

type dispatchArgs struct {
	Task           string   `json:"task"`
	Tools          []string `json:"tools"`
	ConversationID string   `json:"conversation_id"`
}

func (dispatchCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a dispatchArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("dispatch: parse args: %w", err)
	}
	if a.Task == "" {
		return nil, errors.New("dispatch: 'task' is required")
	}
	if call.Svc.Dispatch == nil {
		return nil, errors.New("dispatch: engine not available")
	}
	// Parent linkage: the surface-injected conversation id wins over the
	// model-supplied argument (the model rarely knows its own id).
	convID := call.ConversationID
	if convID == "" {
		convID = a.ConversationID
	}
	res, err := call.Svc.Dispatch(ctx, dispatch.Spec{
		Mode:           dispatch.Agentic,
		Role:           dispatch.RoleMain,
		Tier:           config.TierEveryday,
		Task:           a.Task,
		Tools:          a.Tools,
		WorkDir:        call.WorkDir,
		ConversationID: convID,
		Interactive:    false,
	})
	if err != nil {
		return nil, err
	}
	return capabilities.NewTextResult(res.Text), nil
}
