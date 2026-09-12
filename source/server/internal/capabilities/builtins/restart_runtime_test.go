package builtins

import (
	"cercano/source/server/internal/capabilities"
	"context"
	"encoding/json"
	"testing"
)

func TestRestartRuntimeCapability(t *testing.T) {
	runtimeCalls, agentCalls := 0, 0
	service := capabilities.Services{RestartRuntime: func(_ context.Context, id string) (json.RawMessage, error) {
		runtimeCalls++
		if id != "instance" {
			t.Fatalf("id=%q", id)
		}
		return json.RawMessage(`{"ok":true,"state":"running"}`), nil
	}, RestartAgent: func(string) error { agentCalls++; return nil }}
	capability := RestartRuntime()
	if capability.Name() != "restart_runtime" || capability.Tier() != capabilities.TierX || !capability.Surfaces().Has(capabilities.SurfaceAgent) {
		t.Fatal("incorrect capability metadata")
	}
	result, err := capability.Execute(t.Context(), &capabilities.Call{Svc: service, Args: json.RawMessage(`{"instance_id":"instance"}`)})
	if err != nil || result.Type != capabilities.ResultJSON || runtimeCalls != 1 || agentCalls != 0 {
		t.Fatalf("incorrect execution: %+v %v", result, err)
	}
	if _, err := capability.Execute(t.Context(), &capabilities.Call{Svc: service, Args: json.RawMessage(`{"instance_id":123}`)}); err == nil || runtimeCalls != 1 {
		t.Fatal("invalid arguments executed")
	}
	if _, err := capability.Execute(t.Context(), &capabilities.Call{}); err == nil {
		t.Fatal("missing service accepted")
	}
	reg := capabilities.NewRegistry(service)
	Register(reg)
	if _, ok := reg.Get("restart_runtime"); !ok {
		t.Fatal("runtime tool not registered")
	}
}
