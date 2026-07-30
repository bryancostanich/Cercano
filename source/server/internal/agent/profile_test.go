package agent

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// --- the predicate itself --------------------------------------------------

func TestProfile_ZeroValueRestrictsNothing(t *testing.T) {
	var p Profile
	if p.Restricts() {
		t.Fatal("zero profile must not restrict")
	}
	if !p.Allows(llm.PermW, "Write") || !p.Allows(llm.PermX, "bash") {
		t.Fatal("zero profile must allow every tier")
	}
}

func TestPlanProfile_AllowsReadAndFileWritesOnly(t *testing.T) {
	p := PlanProfile()
	if !p.Restricts() {
		t.Fatal("plan profile must restrict")
	}
	cases := []struct {
		tier llm.Permission
		name string
		want bool
	}{
		{llm.PermR, "LS", true},                    // read tier — allowed
		{llm.PermR, "read_file", true},             // read tier — allowed
		{llm.PermW, "Write", true},                 // file write — permitted to author the plan (display alias)
		{llm.PermW, "write_file", true},            // file write — permitted (capability name)
		{llm.PermW, "Edit", true},                  // file edit — permitted to author the plan
		{llm.PermX, "request_plan_approval", true}, // handoff tool — permitted to leave planning after approval
		{llm.PermX, "bash", false},                 // exec tier — fenced
		{llm.PermX, "Bash", false},                 // exec tier — fenced (display alias)
		{llm.PermW, "git_commit", false},           // non-file write tool — fenced
		{llm.PermW, "Checkpoint", false},           // git mutation — fenced
	}
	for _, c := range cases {
		if got := p.Allows(c.tier, c.name); got != c.want {
			t.Errorf("Allows(%s,%q) = %v, want %v", c.tier, c.name, got, c.want)
		}
	}
}

// --- D: advertisement filter ----------------------------------------------

func TestPlanProfile_FiltersExecToolsButKeepsFileWrites(t *testing.T) {
	reg := testDefaultRegistry() // includes LS (R), Write (W), Bash (X)
	full := agenttools.BuildToolCatalog(reg)
	filtered := agenttools.BuildToolCatalogFiltered(reg, PlanProfile().Allows)

	if !hasTool(full, "Bash") {
		t.Fatal("precondition: unfiltered catalog should advertise Bash")
	}
	// Exec tool is fenced — not advertised while planning.
	if hasTool(filtered, "Bash") {
		t.Fatal("plan profile must NOT advertise the Bash (X) tool to the model")
	}
	// File-write tools ARE advertised — the model authors spec.md/plan.md with them.
	if !hasTool(filtered, "Write") {
		t.Fatal("plan profile must advertise Write so the agent can author the plan")
	}
	if !hasTool(filtered, "Edit") {
		t.Fatal("plan profile must advertise Edit so the agent can revise the plan")
	}
	if !hasTool(filtered, "request_plan_approval") {
		t.Fatal("plan profile must advertise request_plan_approval so the agent can raise the execution handoff")
	}
	// Read tools remain.
	if !hasTool(filtered, "LS") {
		t.Fatal("plan profile must still advertise read-tier tools like LS")
	}
}

func hasTool(cat []llm.Tool, name string) bool {
	for _, tl := range cat {
		if tl.Name == name {
			return true
		}
	}
	return false
}

// --- C: enforcement fence --------------------------------------------------

// An EXEC tool call that reaches the loop despite the D filter (here: injected
// directly via the scripted provider, standing in for a hallucinated name,
// replayed turn, or future code path) must be DENIED outright by the fence —
// no confirm prompt, no execution — with an error tool_result. File writes are
// permitted while planning, so the forbidden tool here is Bash (exec).
func TestPlanProfile_DeniesExecAtGate_NoConfirm(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{
				{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "Bash",
					ToolInput: json.RawMessage(`{"cmd":["rm","-rf","/tmp/x"]}`)},
			},
			// After the fence denies the write, the loop feeds the error result
			// back and the model responds — this second turn is that response.
			{{Type: llm.BlockText, Text: "understood, staying read-only"}},
		},
		caps: inference.Capabilities{SupportsTools: true},
	}
	reg := testDefaultRegistry()
	dir := t.TempDir()
	perms, _ := LoadPermissionStore(dir + "/perms.yaml")

	// If the fence fails and we fall through to the confirm gate, this requester
	// firing is the bug — record it.
	confirmed := false
	requester := func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission, destructive bool) (bool, error) {
		confirmed = true
		return true, nil // even if asked, "yes" — the fence must still block
	}

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		Profile:             PlanProfile(),
		PermissionRequester: requester,
		UserInput:           "run something",
	})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed {
		t.Fatal("fence breach: the confirm gate was reached for an exec tool under the plan profile")
	}

	// The Bash must never have executed. (Belt: the tool_result must be an error
	// naming the profile block.)
	var blocked bool
	for _, m := range result.History {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult && b.ToolUseRef == "u1" {
				if !b.IsError {
					t.Fatal("expected an error tool_result for the blocked Bash")
				}
				blocked = true
			}
		}
	}
	if !blocked {
		t.Fatal("no tool_result recorded for the blocked Bash call")
	}
}

// The load-bearing refined-B behavior: under the plan profile the agent CAN
// author the plan by writing a file with the ordinary Write tool — no confirm,
// it just runs. This is how spec.md/plan.md get written during generation.
func TestPlanProfile_AllowsFileWriteThrough(t *testing.T) {
	dir := t.TempDir()
	planPath := dir + "/plan.md"
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "Write",
				ToolInput: json.RawMessage(`{"path":"` + planPath + `","content":"# Effort\n\n## Phase 1\n- [ ] do it\n"}`)}},
			{{Type: llm.BlockText, Text: "plan written"}},
		},
		caps: inference.Capabilities{SupportsTools: true},
	}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(dir + "/perms.yaml")

	// A requester that would DENY — proving the write did not even need a confirm
	// (file writes are inside the plan profile's allowance, not gated by it).
	requester := func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission, destructive bool) (bool, error) {
		return false, nil
	}

	result, err := RunToolLoop(context.Background(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		Profile:             PlanProfile(),
		PermissionRequester: requester,
		UserInput:           "write the plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "plan written" {
		t.Fatalf("write should have run under plan profile; final=%q", result.FinalText)
	}
	// The file actually exists on disk — the write executed, was not fenced.
	if _, statErr := os.ReadFile(planPath); statErr != nil {
		t.Fatalf("plan.md was not written under the plan profile: %v", statErr)
	}
	// And the tool_result is a success, not an error.
	var ok bool
	for _, m := range result.History {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult && b.ToolUseRef == "u1" && !b.IsError {
				ok = true
			}
		}
	}
	if !ok {
		t.Fatal("expected a successful tool_result for the plan Write")
	}
}

// Read-tier tools still run normally under the plan profile — the fence only
// bites W/X.
func TestPlanProfile_AllowsReadToolThrough(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "LS",
				ToolInput: json.RawMessage(`{"path":"."}`)}},
			{{Type: llm.BlockText, Text: "done"}},
		},
		caps: inference.Capabilities{SupportsTools: true},
	}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(context.Background(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		Profile:   PlanProfile(),
		UserInput: "list",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "done" {
		t.Fatalf("read tool should run under plan profile; final=%q", result.FinalText)
	}
	// The LS result must be a non-error tool_result.
	var ran bool
	for _, m := range result.History {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult && b.ToolUseRef == "u1" && !b.IsError {
				ran = true
			}
		}
	}
	if !ran {
		t.Fatal("LS (read tier) should have run and produced a non-error result")
	}
}

func TestSuggestPlan_PromptsInPermissiveMode(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{{
			{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "suggest_plan",
				ToolInput: json.RawMessage(`{"reason":"spans several files"}`)},
		}},
		caps: inference.Capabilities{SupportsTools: true},
	}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml") // default = permissive

	var asked bool
	requester := func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission, destructive bool) (bool, error) {
		asked = true
		if name != "suggest_plan" {
			t.Fatalf("requester name = %q, want suggest_plan", name)
		}
		if tier != llm.PermX {
			t.Fatalf("requester tier = %q, want PermX so Permissive prompts", tier)
		}
		return false, nil // decline; Execute must not run
	}

	result, err := RunToolLoop(context.Background(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		PermissionRequester: requester,
		UserInput:           "do a big thing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !asked {
		t.Fatal("suggest_plan did not reach the confirm gate in default Permissive mode")
	}
	last := result.History[len(result.History)-1]
	if last.Role != llm.RoleUser || len(last.Blocks) == 0 || !last.Blocks[0].IsError {
		t.Fatalf("decline should be returned as an error tool_result; got %+v", last)
	}
}

func TestSuggestPlan_PromptsInBypassMode(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{{
			{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "suggest_plan",
				ToolInput: json.RawMessage(`{"reason":"spans several files"}`)},
		}},
		caps: inference.Capabilities{SupportsTools: true},
	}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")
	if err := perms.SetMode(ModeBypass); err != nil {
		t.Fatal(err)
	}

	var asked bool
	requester := func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission, destructive bool) (bool, error) {
		asked = true
		if name != "suggest_plan" {
			t.Fatalf("requester name = %q, want suggest_plan", name)
		}
		if tier != llm.PermX {
			t.Fatalf("requester tier = %q, want PermX", tier)
		}
		return false, nil
	}

	_, err := RunToolLoop(context.Background(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		PermissionRequester: requester,
		UserInput:           "do a big thing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !asked {
		t.Fatal("suggest_plan did not reach the confirm gate in Bypass mode; X-tier session-control tools must still prompt")
	}
}

func TestRequestPlanApproval_PromptsUnderPlanProfileInPermissiveMode(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{{
			{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "request_plan_approval",
				ToolInput: json.RawMessage(`{"effort":"efforts/demo","summary":"one phase"}`)},
		}},
		caps: inference.Capabilities{SupportsTools: true},
	}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml") // default = permissive

	var asked bool
	requester := func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission, destructive bool) (bool, error) {
		asked = true
		if name != "request_plan_approval" {
			t.Fatalf("requester name = %q, want request_plan_approval", name)
		}
		if tier != llm.PermX {
			t.Fatalf("requester tier = %q, want PermX so Permissive prompts", tier)
		}
		return false, nil // decline; Execute must not drop the profile
	}

	result, err := RunToolLoop(context.Background(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		Profile:             PlanProfile(),
		PermissionRequester: requester,
		UserInput:           "approve plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !asked {
		t.Fatal("request_plan_approval did not reach the confirm gate under PlanProfile in Permissive mode")
	}
	last := result.History[len(result.History)-1]
	if last.Role != llm.RoleUser || len(last.Blocks) == 0 || !last.Blocks[0].IsError {
		t.Fatalf("decline should be returned as an error tool_result; got %+v", last)
	}
}
