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
	// X-tier is load-bearing: Permissive mode only prompts for X-tier tools, and
	// suggest_plan changes session mode, so it must never auto-run.
	if c.Tier() != capabilities.TierX {
		t.Errorf("Tier() = %q, want TierX (so the confirm gate fires in Permissive)", c.Tier())
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
		EnterProfile: func(convID, name string) error { entered = name; return nil },
	}
	args, _ := json.Marshal(map[string]any{"reason": "database migration requires compatibility and rollout decisions"})
	call := &capabilities.Call{Args: args, Svc: svc}

	res, err := SuggestPlan().Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if entered != "plan" {
		t.Fatalf("EnterProfile called with %q, want \"plan\"", entered)
	}
	// The reason is surfaced back to the model in the result text.
	if !strings.Contains(res.Text, "database migration") {
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
	svc := capabilities.Services{EnterProfile: func(convID, n string) error { entered = n; return nil }}
	call := &capabilities.Call{Args: nil, Svc: svc}
	if _, err := SuggestPlan().Execute(context.Background(), call); err != nil {
		t.Fatalf("Execute with no args: %v", err)
	}
	if entered != "plan" {
		t.Fatalf("entered = %q, want plan", entered)
	}
}

func TestSuggestPlanDescriptionRequiresSignificantWork(t *testing.T) {
	desc := SuggestPlan().Description()
	for _, want := range []string{
		"Default to direct execution", "clear, bounded", "even across multiple files",
		"never justify planning mode on their own", "design itself is still open",
		"architecture",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("suggest_plan description missing %q", want)
		}
	}
	for _, stale := range []string{"work would span multiple files or phases", "small, clear, single-file changes"} {
		if strings.Contains(desc, stale) {
			t.Errorf("overbroad planning criterion: %q", stale)
		}
	}
	schema := string(SuggestPlan().Schema())
	if strings.Contains(schema, "spans 4 files") || !strings.Contains(schema, "migration") {
		t.Fatal("reason example must describe consequential planning needs, not file count")
	}
}

// Regression for observed over-suggestion: in a long session the model proposed
// planning 15 times and the user denied 12. The denials shared four shapes --
// the approach was already agreed in chat and only implementation remained; the
// user had issued a bare execute command ("build it", "do it", "fix it"); the
// user asked for "a plan"/"the shape" meaning prose, not planning mode; or a
// bug fix had failed repeatedly and planning was floated instead of debugging.
// Cross-subsystem sequencing was the stated justification for most of them, so
// it must no longer stand alone as a reason.
func TestSuggestPlanDescriptionExcludesObservedFalsePositives(t *testing.T) {
	desc := SuggestPlan().Description()
	for _, want := range []string{
		// Settled design -> execution, regardless of breadth.
		"agreed in conversation",
		"however many subsystems it spans",
		// Bare imperatives are execution instructions.
		"instruction to build, fix, do, or try",
		// "give me a plan" means prose in the reply.
		"asks for prose in your reply",
		"spec or plan file",
		// Keep debugging instead of proposing a design.
		"keep debugging and fixing",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("suggest_plan description must exclude observed false positive; missing %q", want)
		}
	}
	// Breadth alone was the most common bad justification: it must not appear
	// as a standalone planning criterion.
	if strings.Contains(desc, "complex cross-subsystem sequencing") {
		t.Error("cross-subsystem sequencing must not stand alone as a planning criterion")
	}
}
