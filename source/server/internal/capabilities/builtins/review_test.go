package builtins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
)

func TestReview_Meta(t *testing.T) {
	c := Review()
	if c.Name() != "review" {
		t.Errorf("Name() = %q, want %q", c.Name(), "review")
	}
	if c.Tier() != capabilities.TierR {
		t.Errorf("Tier() = %q, want TierR", c.Tier())
	}
	if !c.Surfaces().Has(capabilities.SurfaceAgent) {
		t.Error("missing SurfaceAgent")
	}
	if !c.Surfaces().Has(capabilities.SurfaceMCP) {
		t.Error("missing SurfaceMCP")
	}
}

func TestReview_Execute_NoTools_OneShot(t *testing.T) {
	var captured dispatch.Spec
	svc := capabilities.Services{
		Dispatch: func(_ context.Context, spec dispatch.Spec) (dispatch.Result, error) {
			captured = spec
			return dispatch.Result{Text: "VERDICT: HOLDS\nREASONING: No counter-evidence found."}, nil
		},
	}
	args, _ := json.Marshal(map[string]any{
		"claim": "Go is statically typed",
	})
	call := &capabilities.Call{Args: args, WorkDir: "/proj", Svc: svc}

	res, err := Review().Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if captured.Mode != dispatch.OneShot {
		t.Errorf("Spec.Mode = %v, want OneShot", captured.Mode)
	}
	if captured.Role != dispatch.RoleMain {
		t.Errorf("Spec.Role = %v, want RoleMain", captured.Role)
	}
	if !strings.Contains(captured.Prompt, "REFUTE") {
		t.Errorf("prompt does not contain 'REFUTE': %q", captured.Prompt)
	}
	if !strings.Contains(captured.Prompt, "Go is statically typed") {
		t.Errorf("prompt does not contain the claim: %q", captured.Prompt)
	}
	if !strings.Contains(res.Text, "HOLDS") {
		t.Errorf("result text = %q, want it to contain HOLDS", res.Text)
	}
}

func TestReview_Execute_WithTools_Agentic(t *testing.T) {
	var captured dispatch.Spec
	svc := capabilities.Services{
		Dispatch: func(_ context.Context, spec dispatch.Spec) (dispatch.Result, error) {
			captured = spec
			return dispatch.Result{Text: "VERDICT: REFUTED\nREASONING: Found counter-example."}, nil
		},
	}
	args, _ := json.Marshal(map[string]any{
		"claim": "all tests pass",
		"tools": []string{"read_file"},
	})
	call := &capabilities.Call{Args: args, WorkDir: "/proj", Svc: svc}

	_, err := Review().Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if captured.Mode != dispatch.Agentic {
		t.Errorf("Spec.Mode = %v, want Agentic", captured.Mode)
	}
	if len(captured.Tools) != 1 || captured.Tools[0] != "read_file" {
		t.Errorf("Spec.Tools = %v, want [read_file]", captured.Tools)
	}
}

func TestReview_Execute_EmptyClaim(t *testing.T) {
	svc := capabilities.Services{
		Dispatch: func(_ context.Context, _ dispatch.Spec) (dispatch.Result, error) {
			return dispatch.Result{}, nil
		},
	}
	args, _ := json.Marshal(map[string]any{"claim": ""})
	call := &capabilities.Call{Args: args, WorkDir: "/proj", Svc: svc}

	_, err := Review().Execute(context.Background(), call)
	if err == nil {
		t.Fatal("expected error for empty claim, got nil")
	}
}

func TestReview_Execute_NilDispatch(t *testing.T) {
	svc := capabilities.Services{} // Dispatch is nil
	args, _ := json.Marshal(map[string]any{"claim": "some claim"})
	call := &capabilities.Call{Args: args, WorkDir: "/proj", Svc: svc}

	_, err := Review().Execute(context.Background(), call)
	if err == nil {
		t.Fatal("expected error when Dispatch is nil, got nil")
	}
}
