package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
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
	return "Run a sub-agent: hand off an open-ended task to a bounded tool-use loop over a granted set of tools (default: read-only tools). Returns the sub-agent's final result."
}
func (dispatchCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["task"],
		"properties": {
			"task":            {"type": "string", "description": "Open-ended instruction for the sub-agent tool loop."},
			"tools":           {"type": "array", "items": {"type": "string"}, "description": "Tool or capability names to grant. Omit to default to read-only tools."},
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
	res, err := call.Svc.Dispatch(ctx, dispatch.Spec{
		Mode:           dispatch.Agentic,
		Role:           dispatch.RoleMain,
		Task:           a.Task,
		Tools:          a.Tools,
		WorkDir:        call.WorkDir,
		ConversationID: a.ConversationID,
		Interactive:    false,
	})
	if err != nil {
		return nil, err
	}
	return capabilities.NewTextResult(res.Text), nil
}
