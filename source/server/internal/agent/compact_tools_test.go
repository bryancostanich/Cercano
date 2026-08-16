package agent

import (
	"strings"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

func toolSet(tools []llm.Tool) map[string]bool {
	set := make(map[string]bool, len(tools))
	for _, tool := range tools {
		set[tool.Name] = true
	}
	return set
}

func TestToolLoop_CompactFallbackAdvertisesFewerTools(t *testing.T) {
	fullProv := &callCountingProvider{}
	compactProv := &callCountingProvider{}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	if _, err := RunToolLoop(t.Context(), ToolLoopInput{Provider: fullProv, Registry: reg, Permissions: perms, UserInput: "hi"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RunToolLoop(t.Context(), ToolLoopInput{Provider: compactProv, Registry: reg, Permissions: perms, UserInput: "hi", TightContextFallback: true}); err != nil {
		t.Fatal(err)
	}
	if len(compactProv.lastReq.Tools) == 0 {
		t.Fatal("compact fallback should still advertise core tools")
	}
	if len(compactProv.lastReq.Tools) >= len(fullProv.lastReq.Tools) {
		t.Fatalf("compact fallback should advertise fewer tools: compact=%d full=%d", len(compactProv.lastReq.Tools), len(fullProv.lastReq.Tools))
	}
	set := toolSet(compactProv.lastReq.Tools)
	for _, want := range []string{"Read", "Grep", "Edit", "Bash", "git_status", "checkpoint", "dispatch", "plan_set_status"} {
		if !set[want] {
			t.Fatalf("compact fallback missing core tool %q; tools=%v", want, set)
		}
	}
	if set["git_push"] || set["git_reset_hard"] {
		t.Fatalf("compact fallback should hide high-risk git mutations unless hydrated: %v", set)
	}
}

func TestToolLoop_CompactFallbackHydratesAllowedToolNextIteration(t *testing.T) {
	prov := &mockProvider{
		caps: inference.Capabilities{SupportsTools: true},
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "h1", ToolName: enableToolsName, ToolInput: []byte(`{"tools":["git_push"]}`)}},
			{{Type: llm.BlockText, Text: "ready"}},
		},
	}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	res, err := RunToolLoop(t.Context(), ToolLoopInput{Provider: prov, Registry: reg, Permissions: perms, UserInput: "hi", TightContextFallback: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalText != "ready" {
		t.Fatalf("final text = %q", res.FinalText)
	}
	if len(prov.reqs) < 2 {
		t.Fatalf("expected two model calls, got %d", len(prov.reqs))
	}
	first := toolSet(prov.reqs[0].Tools)
	second := toolSet(prov.reqs[1].Tools)
	if !first[enableToolsName] {
		t.Fatalf("compact fallback should advertise hydration tool, got %v", first)
	}
	if first["git_push"] {
		t.Fatalf("git_push should not be loaded before hydration: %v", first)
	}
	if !second["git_push"] {
		t.Fatalf("git_push should be loaded after hydration: %v", second)
	}
	if !strings.Contains(prov.reqs[0].System, "COMPACT TOOL DIRECTORY") {
		t.Fatalf("compact tool directory missing from system prompt: %q", prov.reqs[0].System)
	}
}

func TestToolLoop_CompactFallbackDeniesHydrationBlockedByProfile(t *testing.T) {
	prov := &mockProvider{
		caps: inference.Capabilities{SupportsTools: true},
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "h1", ToolName: enableToolsName, ToolInput: []byte(`{"tools":["Bash"]}`)}},
			{{Type: llm.BlockText, Text: "done"}},
		},
	}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	_, err := RunToolLoop(t.Context(), ToolLoopInput{Provider: prov, Registry: reg, Permissions: perms, UserInput: "hi", TightContextFallback: true, Profile: PlanProfile()})
	if err != nil {
		t.Fatal(err)
	}
	if len(prov.reqs) < 2 {
		t.Fatalf("expected two requests, got %d", len(prov.reqs))
	}
	if toolSet(prov.reqs[1].Tools)["Bash"] {
		t.Fatalf("Bash must not hydrate through plan profile: %v", toolSet(prov.reqs[1].Tools))
	}
}

func TestToolLoop_CompactFallbackIntersectsActiveProfile(t *testing.T) {
	prov := &callCountingProvider{}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	if _, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider:             prov,
		Registry:             reg,
		Permissions:          perms,
		UserInput:            "hi",
		TightContextFallback: true,
		Profile:              PlanProfile(),
	}); err != nil {
		t.Fatal(err)
	}
	set := toolSet(prov.lastReq.Tools)
	if set["Bash"] {
		t.Fatal("compact fallback must not advertise Bash through the plan profile")
	}
	if !set["Read"] || !set["Write"] || !set["request_plan_approval"] {
		t.Fatalf("plan profile extras/read tools should survive compact intersection: %v", set)
	}
}
