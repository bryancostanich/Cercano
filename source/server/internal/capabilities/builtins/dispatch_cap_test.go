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

// TestDispatchTierFor pins the dynamic tier: read-only grants stay W (silent
// under permissive); write-capable, unknown, and prefix-mangled-unknown
// grants escalate the dispatch call to X so a human confirms the sub-agent's
// toolset once. Display aliases and synonyms resolve like the live registry.
func TestDispatchTierFor(t *testing.T) {
	d := dispatchCap{}
	cases := []struct {
		args string
		want capabilities.Tier
	}{
		{`{"task":"x"}`, capabilities.TierW},                              // default read-only grant
		{`{"task":"x","tools":["Read","Grep","LS"]}`, capabilities.TierW}, // all R
		{`{"task":"x","tools":["get_protocol"]}`, capabilities.TierW},     // canonical R name
		{`{"task":"x","tools":["Read","Edit"]}`, capabilities.TierX},      // W in grant
		{`{"task":"x","tools":["Bash"]}`, capabilities.TierX},             // W via alias
		{`{"task":"x","tools":["git_push"]}`, capabilities.TierX},         // X in grant
		{`{"task":"x","tools":["mcp__oc__Read"]}`, capabilities.TierW},    // prefix-stripped R
		{`{"task":"x","tools":["mystery_tool"]}`, capabilities.TierX},     // unknown → conservative
		{`{"task":"x","tools":["workflow"]}`, capabilities.TierX},         // synonym of dispatch (W)
	}
	for i, c := range cases {
		if got := d.TierFor(json.RawMessage(c.args)); got != c.want {
			t.Errorf("case %d %s: TierFor = %q, want %q", i, c.args, got, c.want)
		}
	}
}
