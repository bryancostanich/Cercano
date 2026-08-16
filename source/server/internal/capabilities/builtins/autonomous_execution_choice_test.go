package builtins

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
)

func TestRequestAutonomousExecution_Meta(t *testing.T) {
	c := RequestAutonomousExecution()
	if c.Name() != "request_autonomous_execution" {
		t.Fatalf("Name() = %q", c.Name())
	}
	if c.Tier() != capabilities.TierX {
		t.Fatalf("Tier() = %q, want TierX", c.Tier())
	}
	if !c.Surfaces().Has(capabilities.SurfaceAgent) || c.Surfaces().Has(capabilities.SurfaceMCP) {
		t.Fatalf("request_autonomous_execution should be agent-only, got %v", c.Surfaces())
	}
}

func TestRequestAutonomousExecution_ExecuteInstructsBriefThenSuggestAutonomous(t *testing.T) {
	res, err := RequestAutonomousExecution().Execute(context.Background(), &capabilities.Call{Args: []byte(`{"effort":"efforts/demo","summary":"three phases","spec_path":"efforts/demo/spec.md","plan_path":"efforts/demo/plan.md"}`)})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"User approved autonomous execution", "Draft a concise autonomous run brief", "call suggest_autonomous", "efforts/demo", "three phases", "spec.md", "plan.md"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("result missing %q: %q", want, res.Text)
		}
	}
}
