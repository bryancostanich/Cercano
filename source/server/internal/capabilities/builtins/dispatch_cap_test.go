package builtins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/pkg/config"
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
	call := &capabilities.Call{Args: args, WorkDir: "/proj", Emit: func(string) {}, Svc: svc}

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
	// A delegated sub-agent is offloadable work: it must resolve like the
	// co-processor (RoleCoproc), so the user's locus mode decides open vs cloud.
	// RoleMain would pin it to the main thread's tier (cloud under cloud_primary),
	// defeating the point of delegating recon off the frontier tier.
	if captured.Role != dispatch.RoleCoproc {
		t.Errorf("Spec.Role = %v, want RoleCoproc", captured.Role)
	}
	// No "tier" arg -> lightest tier by default, so grunt work offloads cheaply.
	if captured.Tier != config.TierFastLight {
		t.Errorf("Spec.Tier = %v, want TierFastLight (default)", captured.Tier)
	}
	if captured.Emit == nil {
		t.Fatal("Spec.Emit is nil; dispatch progress would not reach the parent turn")
	}
	if res.Text != "done" {
		t.Errorf("result text = %q, want %q", res.Text, "done")
	}
}

func TestDispatch_Execute_IncludesRouteHeader(t *testing.T) {
	svc := capabilities.Services{
		Dispatch: func(_ context.Context, spec dispatch.Spec) (dispatch.Result, error) {
			return dispatch.Result{
				Text:         "done",
				Model:        "qwen3-30b-a3b-instruct-2507",
				Provider:     "mistralrs",
				Tier:         string(spec.Tier),
				IsCloud:      false,
				GrantedTools: []string{"Read"},
			}, nil
		},
	}
	args, _ := json.Marshal(map[string]any{"task": "do X", "tools": []string{"Read"}})
	call := &capabilities.Call{Args: args, WorkDir: "/proj", Svc: svc}

	res, err := Dispatch().Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	want := "[sub-agent route: open provider=mistralrs model=qwen3-30b-a3b-instruct-2507 tier=fast_light]"
	if !strings.Contains(res.Text, want) {
		t.Fatalf("result missing route header %q:\n%s", want, res.Text)
	}
	if !strings.Contains(res.Text, "[sub-agent tools: Read]") {
		t.Fatalf("result missing tools header:\n%s", res.Text)
	}
}

func TestDispatch_Execute_TierKnob(t *testing.T) {
	// The "tier" arg expresses reasoning demand only; it must map onto the
	// taxonomy tier without touching Role (location stays RoleCoproc always).
	cases := []struct {
		arg  string
		want config.Tier
	}{
		{"light", config.TierFastLight},
		{"standard", config.TierEveryday},
		{"deep", config.TierMostCapable},
		{"", config.TierFastLight},         // omitted -> lightest
		{"nonsense", config.TierFastLight}, // unrecognized -> lightest
		{"DEEP", config.TierMostCapable},   // case-insensitive
	}
	for _, c := range cases {
		var captured dispatch.Spec
		svc := capabilities.Services{
			Dispatch: func(_ context.Context, spec dispatch.Spec) (dispatch.Result, error) {
				captured = spec
				return dispatch.Result{Text: "ok"}, nil
			},
		}
		args, _ := json.Marshal(map[string]any{"task": "t", "tier": c.arg})
		call := &capabilities.Call{Args: args, WorkDir: "/proj", Svc: svc}
		if _, err := Dispatch().Execute(context.Background(), call); err != nil {
			t.Fatalf("tier=%q: Execute error: %v", c.arg, err)
		}
		if captured.Tier != c.want {
			t.Errorf("tier=%q: Spec.Tier = %v, want %v", c.arg, captured.Tier, c.want)
		}
		if captured.Role != dispatch.RoleCoproc {
			t.Errorf("tier=%q: Spec.Role = %v, want RoleCoproc (tier must not change location)", c.arg, captured.Role)
		}
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
