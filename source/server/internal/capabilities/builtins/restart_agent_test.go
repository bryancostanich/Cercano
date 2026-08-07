package builtins

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/capabilities"
)

func TestRestartAgent_Meta(t *testing.T) {
	c := RestartAgent()
	if c.Name() != "restart_agent" {
		t.Errorf("Name() = %q", c.Name())
	}
	// X-tier is load-bearing: the bounce severs every in-flight turn and every
	// attached CLI connection, so it must always confirm at the y/n/d/c gate.
	if c.Tier() != capabilities.TierX {
		t.Errorf("Tier() = %q, want TierX", c.Tier())
	}
	if !c.Surfaces().Has(capabilities.SurfaceAgent) {
		t.Error("missing SurfaceAgent")
	}
	// Restarting the host process from an external MCP client is not meaningful.
	if c.Surfaces().Has(capabilities.SurfaceMCP) {
		t.Error("restart_agent must NOT be exposed over MCP")
	}
}

func TestRestartAgent_Execute_InvokesHookWithReason(t *testing.T) {
	var gotReason string
	var called bool
	svc := capabilities.Services{RestartAgent: func(reason string) error {
		called = true
		gotReason = reason
		return nil
	}}
	args, _ := json.Marshal(map[string]any{"reason": "binary rebuilt"})
	call := &capabilities.Call{Args: args, Svc: svc}

	res, err := RestartAgent().Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !called {
		t.Fatal("RestartAgent hook was not invoked")
	}
	if gotReason != "binary rebuilt" {
		t.Fatalf("hook reason = %q, want %q", gotReason, "binary rebuilt")
	}
	if res == nil || res.Text == "" {
		t.Fatal("expected a non-empty result")
	}
}

func TestRestartAgent_Execute_NoArgsUsesDefaultReason(t *testing.T) {
	var gotReason string
	svc := capabilities.Services{RestartAgent: func(reason string) error {
		gotReason = reason
		return nil
	}}
	call := &capabilities.Call{Args: nil, Svc: svc}

	if _, err := RestartAgent().Execute(context.Background(), call); err != nil {
		t.Fatalf("Execute with no args: %v", err)
	}
	if gotReason == "" {
		t.Fatal("expected a non-empty default reason")
	}
}

func TestRestartAgent_Execute_NilHookErrors(t *testing.T) {
	call := &capabilities.Call{Args: json.RawMessage(`{}`), Svc: capabilities.Services{}}
	if _, err := RestartAgent().Execute(context.Background(), call); err == nil {
		t.Fatal("expected an error when RestartAgent hook is nil")
	}
}
