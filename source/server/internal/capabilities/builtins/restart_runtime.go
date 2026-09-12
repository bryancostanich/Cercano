package builtins

import (
	"cercano/source/server/internal/capabilities"
	"context"
	"encoding/json"
	"fmt"
)

type restartRuntimeCap struct{}

func (restartRuntimeCap) Name() string { return "restart_runtime" }
func (restartRuntimeCap) Description() string {
	return "Restart only a managed llama-server runtime through the existing runtime RPC; never restart the Cercano agent. Optional instance_id selects an exact instance; omission is allowed only when exactly one llama-server instance exists. Always confirms. Preserves context settings and memory guards. A failed restart may leave the runtime stopped, which the result reports. Callable in normal mode; advertised in /d debug mode."
}
func (restartRuntimeCap) Tier() capabilities.Tier        { return capabilities.TierX }
func (restartRuntimeCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }
func (restartRuntimeCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{"type":"object","properties":{"instance_id":{"type":"string","description":"Exact managed llama-server instance ID. Omit only to select the sole llama-server instance."}},"additionalProperties":false}`)
}
func (restartRuntimeCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var args struct {
		InstanceID string `json:"instance_id"`
	}
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return nil, err
		}
	}
	if call.Svc.RestartRuntime == nil {
		return nil, fmt.Errorf("runtime restart service not available")
	}
	result, err := call.Svc.RestartRuntime(ctx, args.InstanceID)
	if err != nil {
		return nil, err
	}
	return &capabilities.Result{Type: capabilities.ResultJSON, JSON: result}, nil
}

var _ capabilities.Capability = restartRuntimeCap{}

func RestartRuntime() capabilities.Capability { return restartRuntimeCap{} }
