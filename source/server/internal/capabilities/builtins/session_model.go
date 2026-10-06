package builtins

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/chatroute"
)

type sessionModelCap struct{}

func SessionModel() capabilities.Capability { return sessionModelCap{} }
func (sessionModelCap) Name() string        { return "session_model" }
func (sessionModelCap) Description() string {
	return "Inspect, set, or clear this conversation's main-chat model override. Use status first to discover saved account/profile names, then models with a profile to discover exact model IDs (where supported). Do not guess IDs from display names. Set requires an exact provider model ID, not a display name. Changes apply starting with the NEXT user message, persist on resume, and never change global, delegation, or background routing. An explicit override never silently falls back. Use clear to restore normal routing. Never switch without the user's request."
}
func (sessionModelCap) Tier() capabilities.Tier { return capabilities.TierX }
func (sessionModelCap) TierFor(args json.RawMessage) capabilities.Tier {
	var req chatroute.Request
	if json.Unmarshal(args, &req) == nil && ((req.Action == "" || req.Action == "status") && req.Profile == "" || req.Action == "models") && req.Model == "" {
		return capabilities.TierR
	}
	return capabilities.TierX
}
func (sessionModelCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }
func (sessionModelCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{"type":"object","properties":{"action":{"type":"string","enum":["status","models","set","clear"],"description":"Defaults to status. Models lists the named profile catalog. Set and clear require confirmation."},"profile":{"type":"string","description":"Exact saved account/profile name, required for set or models."},"model":{"type":"string","description":"Exact model ID accepted by that provider, required for set."}},"additionalProperties":false}`)
}
func (sessionModelCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var req chatroute.Request
	if err := json.Unmarshal(call.Args, &req); err != nil {
		return nil, err
	}
	if call.Svc.SessionModel == nil {
		return nil, fmt.Errorf("session model control unavailable")
	}
	if call.ConversationID == "" {
		return nil, fmt.Errorf("conversation id required")
	}
	result, err := call.Svc.SessionModel(ctx, call.ConversationID, req)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return &capabilities.Result{Type: capabilities.ResultJSON, JSON: data}, nil
}
