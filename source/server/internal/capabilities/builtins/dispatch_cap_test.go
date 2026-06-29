package builtins

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
)

func TestDispatch_Meta(t *testing.T) {
	c := Dispatch()
	if c.Name() != "dispatch" {
		t.Errorf("Name() = %q, want %q", c.Name(), "dispatch")
	}
	if c.Tier() != capabilities.TierW {
		t.Errorf("Tier() = %q, want TierW", c.Tier())
	}
	if !c.Surfaces().Has(capabilities.SurfaceAgent) {
		t.Error("missing SurfaceAgent")
	}
	if !c.Surfaces().Has(capabilities.SurfaceMCP) {
		t.Error("missing SurfaceMCP")
	}
}

func TestDispatch_Execute_ForwardsSpec(t *testing.T) {
	var captured dispatch.Spec
	svc := capabilities.Services{
		Dispatch: func(_ context.Context, spec dispatch.Spec) (dispatch.Result, error) {
			captured = spec
			return dispatch.Result{Text: "done"}, nil
		},
	}
	args, _ := json.Marshal(map[string]any{
		"task":  "do X",
		"tools": []string{"read_file"},
	})
	call := &capabilities.Call{Args: args, WorkDir: "/proj", Svc: svc}

	res, err := Dispatch().Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if captured.Mode != dispatch.Agentic {
		t.Errorf("Spec.Mode = %v, want Agentic", captured.Mode)
	}
	if captured.Task != "do X" {
		t.Errorf("Spec.Task = %q, want %q", captured.Task, "do X")
	}
	if len(captured.Tools) != 1 || captured.Tools[0] != "read_file" {
		t.Errorf("Spec.Tools = %v, want [read_file]", captured.Tools)
	}
	if captured.Role != dispatch.RoleMain {
		t.Errorf("Spec.Role = %v, want RoleMain", captured.Role)
	}
	if res.Text != "done" {
		t.Errorf("result text = %q, want %q", res.Text, "done")
	}
}

func TestDispatch_Execute_EmptyTask(t *testing.T) {
	svc := capabilities.Services{
		Dispatch: func(_ context.Context, _ dispatch.Spec) (dispatch.Result, error) {
			return dispatch.Result{}, nil
		},
	}
	args, _ := json.Marshal(map[string]any{"task": ""})
	call := &capabilities.Call{Args: args, WorkDir: "/proj", Svc: svc}

	_, err := Dispatch().Execute(context.Background(), call)
	if err == nil {
		t.Fatal("expected error for empty task, got nil")
	}
}

func TestDispatch_Execute_NilDispatch(t *testing.T) {
	svc := capabilities.Services{} // Dispatch is nil
	args, _ := json.Marshal(map[string]any{"task": "something"})
	call := &capabilities.Call{Args: args, WorkDir: "/proj", Svc: svc}

	_, err := Dispatch().Execute(context.Background(), call)
	if err == nil {
		t.Fatal("expected error when Dispatch is nil, got nil")
	}
}
