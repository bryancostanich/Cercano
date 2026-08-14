package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
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

// stubTool is a minimal Tool used to exercise loop mechanics without pulling in
// capability/Services wiring. Its Execute always succeeds and flips ran=true.
type stubTool struct {
	name string
	perm agenttools.Permission
	ran  *bool
}

func (s stubTool) Name() string                      { return s.name }
func (s stubTool) Description() string               { return s.name }
func (s stubTool) Permission() agenttools.Permission { return s.perm }
func (s stubTool) Schema() json.RawMessage           { return json.RawMessage(`{"type":"object"}`) }
func (s stubTool) Execute(ctx context.Context, args json.RawMessage) (*agenttools.Result, error) {
	if s.ran != nil {
		// The same stubTool can be invoked concurrently when a turn scripts the
		// same tool name more than once and the provider supports parallel tool
		// calls (see TestToolLoop_CalledToolsDeduplicatesRepeatedTool). Guard the
		// shared *ran write so -race stays clean; the post-loop reads are safe
		// because RunToolLoop joins all tool goroutines before returning.
		stubRanMu.Lock()
		*s.ran = true
		stubRanMu.Unlock()
	}
	return agenttools.NewTextResult("ok"), nil
}

// stubRanMu serializes writes to stubTool.ran across concurrent Execute calls.
var stubRanMu sync.Mutex

// A successful plan_exit call must lift the read-only planning fence for the
// REST of the same turn. The ProfileBroker change plan_exit makes only lands on
// the next turn; without the loop-local relaxation the remainder of this turn
// stays fenced and a following non-file W-tier tool is wrongly blocked. Here a
// plan_exit (turn 1) is followed by a plain W-tier tool (turn 2) that is NOT in
// the plan profile's allowance — it must run because the fence was dropped.
func TestPlanExit_LiftsFenceForRestOfTurn(t *testing.T) {
	var saved bool
	reg := agenttools.NewRegistry()
	// plan_exit stub: in planExtraTools, so it's allowed and executes; W-tier.
	reg.MustRegister(stubTool{name: "plan_exit", perm: agenttools.PermW})
	// save_note: a non-file W tool — fenced by PlanProfile unless the fence is
	// lifted. It records whether it actually ran.
	reg.MustRegister(stubTool{name: "save_note", perm: agenttools.PermW, ran: &saved})

	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "plan_exit",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockToolUse, ToolUseID: "u2", ToolName: "save_note",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockText, Text: "done"}},
		},
		caps: inference.Capabilities{SupportsTools: true},
	}
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	// A requester that DENIES — proving save_note ran because the fence was
	// lifted (unrestricted W runs without a confirm), not because it was
	// confirmed. (If the fence were still up, save_note would be denied at the
	// gate with an error result, never reaching Execute.)
	requester := func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission, destructive bool) (bool, error) {
		return false, nil
	}

	result, err := RunToolLoop(context.Background(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		Profile:             PlanProfile(),
		PermissionRequester: requester,
		UserInput:           "exit planning then save a note",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !saved {
		t.Fatal("save_note did not execute after plan_exit — the planning fence was not lifted for the rest of the turn")
	}
	if result.FinalText != "done" {
		t.Fatalf("loop did not complete cleanly; final=%q", result.FinalText)
	}
}

// A successful request_plan_approval call must lift the read-only planning fence
// for the REST of the same turn, exactly like plan_exit. Approving the plan
// flips the ProfileBroker to the default profile, but that only lands on the
// next turn; without the loop-local relaxation the model believes it has left
// planning mode yet every follow-on write/exec tool stays fenced for the rest
// of this turn. This is the request_plan_approval sibling of the plan_exit case
// above — both must lift the fence via agent.ToolLiftsPlanFence.
func TestRequestPlanApproval_LiftsFenceForRestOfTurn(t *testing.T) {
	var saved bool
	reg := agenttools.NewRegistry()
	// request_plan_approval stub: in planExtraTools, so it's allowed; X-tier in
	// production but W here is enough to exercise the mid-turn fence-lift.
	reg.MustRegister(stubTool{name: "request_plan_approval", perm: agenttools.PermW})
	// save_note: a non-file W tool — fenced by PlanProfile unless the fence is
	// lifted. It records whether it actually ran.
	reg.MustRegister(stubTool{name: "save_note", perm: agenttools.PermW, ran: &saved})

	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "request_plan_approval",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockToolUse, ToolUseID: "u2", ToolName: "save_note",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockText, Text: "done"}},
		},
		caps: inference.Capabilities{SupportsTools: true},
	}
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	// A requester that DENIES — proving save_note ran because the fence was
	// lifted (unrestricted W runs without a confirm), not because it was
	// confirmed. If the fence were still up, save_note would be denied at the
	// gate and never reach Execute.
	requester := func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission, destructive bool) (bool, error) {
		return false, nil
	}

	result, err := RunToolLoop(context.Background(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		Profile:             PlanProfile(),
		PermissionRequester: requester,
		UserInput:           "approve the plan then save a note",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !saved {
		t.Fatal("save_note did not execute after request_plan_approval — the planning fence was not lifted for the rest of the turn")
	}
	if result.FinalText != "done" {
		t.Fatalf("loop did not complete cleanly; final=%q", result.FinalText)
	}
}

// Guard: without a prior plan_exit, the same non-file W tool IS fenced. This
// pins the fix to plan_exit rather than a blanket relaxation.
func TestPlanProfile_FencesNonFileWriteWithoutPlanExit(t *testing.T) {
	var saved bool
	reg := agenttools.NewRegistry()
	reg.MustRegister(stubTool{name: "save_note", perm: agenttools.PermW, ran: &saved})

	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "save_note",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockText, Text: "understood"}},
		},
		caps: inference.Capabilities{SupportsTools: true},
	}
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")
	requester := func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission, destructive bool) (bool, error) {
		return true, nil // even "yes" must not let a fenced tool run
	}

	if _, err := RunToolLoop(context.Background(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		Profile:             PlanProfile(),
		PermissionRequester: requester,
		UserInput:           "save a note",
	}); err != nil {
		t.Fatal(err)
	}
	if saved {
		t.Fatal("save_note ran under the plan profile with no plan_exit — fence breach")
	}
}

// FU-1a: re-calling suggest_plan while already in planning mode must not return
// the self-contradictory "read-only … unavailable while planning" text. It gets
// a clear "already in planning mode" message pointing at the next step.
func TestFenceDenialMessage_SuggestPlanUnderPlanProfile_IsSelfAware(t *testing.T) {
	msg := fenceDenialMessage("plan", "suggest_plan", llm.PermX)
	if !strings.Contains(msg, "already in planning mode") {
		t.Fatalf("suggest_plan denial should say it's already planning; got: %q", msg)
	}
	// It must NOT claim suggest_plan itself is "unavailable" — that's the
	// self-contradiction (it was trying to plan). It should instead redirect.
	if strings.Contains(msg, "unavailable") {
		t.Fatalf("suggest_plan denial should not say the tool is unavailable; got: %q", msg)
	}
	if !strings.Contains(msg, "request_plan_approval") || !strings.Contains(msg, "plan_exit") {
		t.Fatalf("suggest_plan denial should point at the exits; got: %q", msg)
	}

	// A genuinely fenced write still gets the plain "unavailable" explanation.
	other := fenceDenialMessage("plan", "run_command", llm.PermX)
	if !strings.Contains(other, "unavailable") || !strings.Contains(other, "run_command") {
		t.Fatalf("non-planning tool denial should keep the read-only explanation; got: %q", other)
	}
}
