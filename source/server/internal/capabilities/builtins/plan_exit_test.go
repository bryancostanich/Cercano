package builtins

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/capabilities"
)

func TestPlanExit_Meta(t *testing.T) {
	c := PlanExit()
	if c.Name() != "plan_exit" {
		t.Errorf("Name() = %q", c.Name())
	}
	// W-tier is load-bearing: exiting planning mode only lifts a restriction, so
	// it must exit silently with no confirm gate. X-tier would prompt.
	if c.Tier() != capabilities.TierW {
		t.Errorf("Tier() = %q, want TierW", c.Tier())
	}
	if !c.Surfaces().Has(capabilities.SurfaceAgent) {
		t.Error("missing SurfaceAgent")
	}
	if c.Surfaces().Has(capabilities.SurfaceMCP) {
		t.Error("plan_exit must NOT be exposed over MCP")
	}
}

func TestPlanExit_Execute_LeavesPlanningProfile(t *testing.T) {
	var entered string
	svc := capabilities.Services{EnterProfile: func(name string) error { entered = name; return nil }}
	args, _ := json.Marshal(map[string]any{"reason": "no plan needed"})
	call := &capabilities.Call{Args: args, Svc: svc}

	res, err := PlanExit().Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if entered != "default" {
		t.Fatalf("EnterProfile called with %q, want default", entered)
	}
	if res == nil || res.Text == "" {
		t.Fatal("expected a non-empty result")
	}
}

func TestPlanExit_Execute_NoArgsStillLeavesPlanning(t *testing.T) {
	var entered string
	svc := capabilities.Services{EnterProfile: func(n string) error { entered = n; return nil }}
	call := &capabilities.Call{Args: nil, Svc: svc}
	if _, err := PlanExit().Execute(context.Background(), call); err != nil {
		t.Fatalf("Execute with no args: %v", err)
	}
	if entered != "default" {
		t.Fatalf("entered = %q, want default", entered)
	}
}

func TestPlanExit_Execute_NilHookErrors(t *testing.T) {
	call := &capabilities.Call{Args: json.RawMessage(`{}`), Svc: capabilities.Services{}}
	if _, err := PlanExit().Execute(context.Background(), call); err == nil {
		t.Fatal("expected an error when EnterProfile is nil")
	}
}
