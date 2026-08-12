package builtins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
)

func TestRequestPlanApproval_Meta(t *testing.T) {
	c := RequestPlanApproval()
	if c.Name() != "request_plan_approval" {
		t.Errorf("Name() = %q", c.Name())
	}
	// X-tier is load-bearing: Permissive mode only prompts for X-tier tools, and
	// this capability changes session mode, so it must never auto-run.
	if c.Tier() != capabilities.TierX {
		t.Errorf("Tier() = %q, want TierX", c.Tier())
	}
	if !c.Surfaces().Has(capabilities.SurfaceAgent) {
		t.Error("missing SurfaceAgent")
	}
	if c.Surfaces().Has(capabilities.SurfaceMCP) {
		t.Error("request_plan_approval must NOT be exposed over MCP")
	}
}

func TestRequestPlanApproval_Execute_LeavesPlanningProfile(t *testing.T) {
	var entered string
	svc := capabilities.Services{EnterProfile: func(convID, name string) error { entered = name; return nil }}
	args, _ := json.Marshal(map[string]any{
		"effort":    "efforts/migrate-config-loader",
		"summary":   "Three phases: loader, migration, cleanup.",
		"spec_path": "efforts/migrate-config-loader/spec.md",
		"plan_path": "efforts/migrate-config-loader/plan.md",
	})
	call := &capabilities.Call{Args: args, Svc: svc}

	res, err := RequestPlanApproval().Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if entered != "default" {
		t.Fatalf("EnterProfile called with %q, want default", entered)
	}
	for _, must := range []string{"Plan approved", "efforts/migrate-config-loader", "loader", "spec.md", "plan.md"} {
		if !strings.Contains(res.Text, must) {
			t.Fatalf("result %q missing %q", res.Text, must)
		}
	}
}

func TestRequestPlanApproval_Execute_NilHookErrors(t *testing.T) {
	call := &capabilities.Call{Args: json.RawMessage(`{}`), Svc: capabilities.Services{}}
	if _, err := RequestPlanApproval().Execute(context.Background(), call); err == nil {
		t.Fatal("expected an error when EnterProfile is nil")
	}
}

func TestRequestPlanApproval_Execute_NoArgsStillLeavesPlanning(t *testing.T) {
	var entered string
	svc := capabilities.Services{EnterProfile: func(convID, n string) error { entered = n; return nil }}
	call := &capabilities.Call{Args: nil, Svc: svc}
	if _, err := RequestPlanApproval().Execute(context.Background(), call); err != nil {
		t.Fatalf("Execute with no args: %v", err)
	}
	if entered != "default" {
		t.Fatalf("entered = %q, want default", entered)
	}
}
