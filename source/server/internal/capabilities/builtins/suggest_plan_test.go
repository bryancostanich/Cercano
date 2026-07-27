package builtins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
)

func TestSuggestPlan_Meta(t *testing.T) {
	c := SuggestPlan()
	if c.Name() != "suggest_plan" {
		t.Errorf("Name() = %q", c.Name())
	}
	// W-tier is load-bearing: it is what makes the confirm gate fire BEFORE
	// Execute, which is the user-facing y/n/d/c suggestion prompt.
	if c.Tier() != capabilities.TierW {
		t.Errorf("Tier() = %q, want TierW (so the confirm gate fires)", c.Tier())
	}
	// Agent surface only — entering a session mode is meaningless over MCP.
	if !c.Surfaces().Has(capabilities.SurfaceAgent) {
		t.Error("missing SurfaceAgent")
	}
	if c.Surfaces().Has(capabilities.SurfaceMCP) {
		t.Error("suggest_plan must NOT be exposed over MCP")
	}
}

// Reaching Execute means the user approved at the gate; Execute must flip the
// session into the plan profile via the injected hook.
func TestSuggestPlan_Execute_EntersPlanProfile(t *testing.T) {
	var entered string
	svc := capabilities.Services{
		EnterProfile: func(name string) error { entered = name; return nil },
	}
	args, _ := json.Marshal(map[string]any{"reason": "spans 4 files; approach uncertain"})
	call := &capabilities.Call{Args: args, Svc: svc}

	res, err := SuggestPlan().Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if entered != "plan" {
		t.Fatalf("EnterProfile called with %q, want \"plan\"", entered)
	}
	// The reason is surfaced back to the model in the result text.
	if !strings.Contains(res.Text, "spans 4 files") {
		t.Errorf("result should echo the reason; got %q", res.Text)
	}
}

// A missing EnterProfile hook (e.g. in a sub-agent worker) must error LOUDLY,
// never silently pretend planning happened.
func TestSuggestPlan_Execute_NilHookErrors(t *testing.T) {
	call := &capabilities.Call{Args: json.RawMessage(`{}`), Svc: capabilities.Services{}}
	if _, err := SuggestPlan().Execute(context.Background(), call); err == nil {
		t.Fatal("expected an error when EnterProfile is nil")
	}
}

// Empty/absent args are tolerated (reason is optional).
func TestSuggestPlan_Execute_NoArgs(t *testing.T) {
	var entered string
	svc := capabilities.Services{EnterProfile: func(n string) error { entered = n; return nil }}
	call := &capabilities.Call{Args: nil, Svc: svc}
	if _, err := SuggestPlan().Execute(context.Background(), call); err != nil {
		t.Fatalf("Execute with no args: %v", err)
	}
	if entered != "plan" {
		t.Fatalf("entered = %q, want plan", entered)
	}
}
