package agent

import (
	"cercano/source/server/internal/agenttools"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type debugRuntimeTool struct{ called *bool }

func (debugRuntimeTool) Name() string                      { return "restart_runtime" }
func (debugRuntimeTool) Description() string               { return "restart llama-server" }
func (debugRuntimeTool) Permission() agenttools.Permission { return agenttools.PermX }
func (debugRuntimeTool) Schema() json.RawMessage           { return json.RawMessage(`{"type":"object"}`) }
func (t debugRuntimeTool) Execute(context.Context, json.RawMessage) (*agenttools.Result, error) {
	*t.called = true
	return &agenttools.Result{}, nil
}
func TestDebugAdvertisementDoesNotRestrictAvailability(t *testing.T) {
	called := false
	reg := agenttools.NewRegistry()
	reg.MustRegister(debugRuntimeTool{&called})
	for _, tight := range []bool{false, true} {
		for _, debug := range []bool{false, true} {
			catalog := buildCompactToolCatalog(reg, Profile{}, tight, map[string]bool{"restart_runtime": true}, debug)
			if toolSet(catalog)["restart_runtime"] != debug {
				t.Fatalf("debug=%v tight=%v has incorrect catalog", debug, tight)
			}
			directory := compactToolDirectory(reg, Profile{}, nil, debug)
			if !debug && strings.Contains(directory, "restart_runtime") {
				t.Fatal("debug tool leaked into compact directory")
			}
		}
	}
	tool, ok := reg.Get("restart_runtime")
	if !ok || tool.Permission() != agenttools.PermX {
		t.Fatal("registration/permission changed")
	}
	if _, err := tool.Execute(t.Context(), nil); err != nil || !called {
		t.Fatal("hidden tool is no longer callable")
	}
}
func TestToolLoopDebugFlagControlsAdvertisement(t *testing.T) {
	for _, debug := range []bool{false, true} {
		provider := &callCountingProvider{}
		reg := agenttools.NewRegistry()
		called := false
		reg.MustRegister(debugRuntimeTool{&called})
		perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")
		if _, err := RunToolLoop(t.Context(), ToolLoopInput{Provider: provider, Registry: reg, Permissions: perms, UserInput: "hello", DebugMode: debug}); err != nil {
			t.Fatal(err)
		}
		if toolSet(provider.lastReq.Tools)["restart_runtime"] != debug {
			t.Fatal("loop did not propagate debug advertisement")
		}
	}
}

type debugReasoningTool struct{ debugRuntimeTool }

func (debugReasoningTool) Name() string { return "reasoning_diagnostic" }
func TestReasoningDiagnosticDebugAdvertisementAndConfirmation(t *testing.T) {
	reg := agenttools.NewRegistry()
	called := false
	reg.MustRegister(debugReasoningTool{debugRuntimeTool{&called}})
	for _, tight := range []bool{false, true} {
		for _, debug := range []bool{false, true} {
			catalog := buildCompactToolCatalog(reg, Profile{}, tight, nil, debug)
			if toolSet(catalog)["reasoning_diagnostic"] != debug {
				t.Fatal("incorrect debug advertisement")
			}
			if !debug && strings.Contains(compactToolDirectory(reg, Profile{}, nil, debug), "reasoning_diagnostic") {
				t.Fatal("tool leaked into directory")
			}
		}
	}
	for _, mode := range []PermissionMode{ModeStrict, ModePermissive, ModeBypass} {
		if !GateDecisionForTool(mode, "X", "reasoning_diagnostic", false, true) {
			t.Fatal("paid experiment not confirmed")
		}
	}
}
