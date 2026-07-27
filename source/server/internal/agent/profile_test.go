package agent

import (
	"context"
	"encoding/json"
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

func TestPlanProfile_AllowsReadAndPlanOnly(t *testing.T) {
	p := PlanProfile()
	if !p.Restricts() {
		t.Fatal("plan profile must restrict")
	}
	cases := []struct {
		tier llm.Permission
		name string
		want bool
	}{
		{llm.PermR, "LS", true},               // read tier — allowed
		{llm.PermR, "read_file", true},        // read tier — allowed
		{llm.PermW, "Write", false},           // write tier — fenced
		{llm.PermX, "bash", false},            // exec tier — fenced
		{llm.PermW, PlanCapabilityName, true}, // plan cap — escape hatch even though not PermR
	}
	for _, c := range cases {
		if got := p.Allows(c.tier, c.name); got != c.want {
			t.Errorf("Allows(%s,%q) = %v, want %v", c.tier, c.name, got, c.want)
		}
	}
}

// --- D: advertisement filter ----------------------------------------------

func TestPlanProfile_FiltersWriteToolsFromCatalog(t *testing.T) {
	reg := testDefaultRegistry() // includes LS (R) and Write (W)
	full := agenttools.BuildToolCatalog(reg)
	filtered := agenttools.BuildToolCatalogFiltered(reg, PlanProfile().Allows)

	if !hasTool(full, "Write") {
		t.Fatal("precondition: unfiltered catalog should advertise Write")
	}
	if hasTool(filtered, "Write") {
		t.Fatal("plan profile must NOT advertise the Write (W) tool to the model")
	}
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

// A write tool call that reaches the loop despite the D filter (here: injected
// directly via the scripted provider, standing in for a hallucinated name,
// replayed turn, or future code path) must be DENIED outright by the fence —
// no confirm prompt, no execution — with an error tool_result.
func TestPlanProfile_DeniesWriteAtGate_NoConfirm(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{
				{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "Write",
					ToolInput: json.RawMessage(`{"path":"/tmp/x","content":"x"}`)},
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
		UserInput:           "write x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed {
		t.Fatal("fence breach: the confirm gate was reached for a write under the plan profile")
	}

	// The Write must never have executed: /tmp/x must not exist. (Belt: the
	// tool_result must be an error naming the profile block.)
	var blocked bool
	for _, m := range result.History {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult && b.ToolUseRef == "u1" {
				if !b.IsError {
					t.Fatal("expected an error tool_result for the blocked Write")
				}
				blocked = true
			}
		}
	}
	if !blocked {
		t.Fatal("no tool_result recorded for the blocked Write call")
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
