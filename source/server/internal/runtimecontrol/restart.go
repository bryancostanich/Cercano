// Package runtimecontrol implements the restart tool through the public runtime
// RPC methods. It never kills processes itself or changes runtime settings.
package runtimecontrol

import (
	"cercano/source/server/pkg/proto"
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type RPC interface {
	GetRuntimeStatus(context.Context, *proto.GetRuntimeStatusRequest) (*proto.GetRuntimeStatusResponse, error)
	RestartRuntime(context.Context, *proto.RestartRuntimeRequest) (*proto.RestartRuntimeResponse, error)
}
type Instance struct {
	ID    string `json:"instance_id"`
	Model string `json:"model_id"`
	State string `json:"state"`
	PID   int32  `json:"pid"`
}
type Result struct {
	OK        bool       `json:"ok"`
	Error     string     `json:"error,omitempty"`
	State     string     `json:"state"`
	OldPID    int32      `json:"old_pid,omitempty"`
	Instance  *Instance  `json:"instance,omitempty"`
	Available []Instance `json:"available_instances,omitempty"`
}

func brief(p *proto.RuntimeInstance) Instance {
	return Instance{ID: p.GetId(), Model: p.GetModelId(), State: p.GetState(), PID: p.GetPid()}
}

func Restart(ctx context.Context, rpc RPC, id string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	status, err := rpc.GetRuntimeStatus(ctx, &proto.GetRuntimeStatusRequest{})
	if err != nil {
		return nil, fmt.Errorf("runtime status: %w", err)
	}
	result := Result{State: "unchanged"}
	existing := make(map[string]bool)
	for _, instance := range status.GetInstances() {
		existing[instance.GetId()] = true
	}
	var selected *proto.RuntimeInstance
	for _, instance := range status.GetInstances() {
		if instance.GetRuntime() != "llama_server" {
			continue
		}
		result.Available = append(result.Available, brief(instance))
		if instance.GetId() == id {
			selected = instance
		}
	}
	if id == "" && len(result.Available) == 1 {
		for _, instance := range status.GetInstances() {
			if instance.GetId() == result.Available[0].ID {
				selected = instance
			}
		}
	}
	if selected == nil {
		result.Error = "Supply a llama_server instance_id from available_instances; no runtime was restarted."
		return json.Marshal(result)
	}
	result.Available = nil
	result.OldPID = selected.GetPid()
	before := brief(selected)
	result.Instance = &before
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	response, restartErr := rpc.RestartRuntime(ctx, &proto.RestartRuntimeRequest{InstanceId: selected.GetId(), Runtime: "llama_server", ModelId: selected.GetModelId()})
	if restartErr == nil && !response.GetOk() {
		restartErr = fmt.Errorf("%s", response.GetError())
	}
	if restartErr == nil && response.GetInstance() != nil {
		current := brief(response.GetInstance())
		result.Instance = &current
		result.State = current.State
		result.OK = true
		return json.Marshal(result)
	}
	if restartErr != nil {
		result.Error = restartErr.Error()
	} else {
		result.Error = "Restart RPC returned no instance; outcome must be checked."
	}
	// Restart may stop the old process before admission fails. Report the actual
	// state without retrying, changing context, or bypassing the memory guard.
	inspectCtx, inspectCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer inspectCancel()
	after, inspectErr := rpc.GetRuntimeStatus(inspectCtx, &proto.GetRuntimeStatusRequest{})
	result.State = "unknown"
	if inspectErr == nil {
		result.State = "stopped"
		result.Instance = nil
		for _, instance := range after.GetInstances() {
			if instance.GetRuntime() == "llama_server" && (instance.GetId() == selected.GetId() || (!existing[instance.GetId()] && instance.GetModelId() == selected.GetModelId())) {
				current := brief(instance)
				result.Instance = &current
				result.State = current.State
				break
			}
		}
	}
	return json.Marshal(result)
}
