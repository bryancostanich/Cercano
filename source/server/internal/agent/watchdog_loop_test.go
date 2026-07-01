package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm"
)

// fakeWTool is a minimal PermW (non-MCP) tool that records whether it ran.
type fakeWTool struct {
	executed bool
}

func (*fakeWTool) Name() string                      { return "edit_file" }
func (*fakeWTool) Description() string               { return "edits a file" }
func (*fakeWTool) Permission() agenttools.Permission { return agenttools.PermW }
func (*fakeWTool) Schema() json.RawMessage           { return json.RawMessage(`{"type":"object"}`) }
func (m *fakeWTool) Execute(_ context.Context, _ json.RawMessage) (*agenttools.Result, error) {
	m.executed = true
	return agenttools.NewTextResult("ok"), nil
}

// TestWatchdogGate_ChallengeSkipsExecution: when the WatchdogGate returns
// "challenge", the W-tier tool must NOT execute and a "watchdog" tool-result is
// injected so the model can react.
func TestWatchdogGate_ChallengeSkipsExecution(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "edit_file",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockText, Text: "understood"}},
		},
		caps: llm.Capabilities{SupportsTools: true},
	}
	tool := &fakeWTool{}
	reg := agenttools.NewRegistry()
	reg.MustRegister(tool)
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms, UserInput: "edit it",
		// A permissive PermissionRequester would allow execution — the gate must
		// preempt it entirely.
		PermissionRequester: func(_ context.Context, _, _ string, _ json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
			return true, nil
		},
		WatchdogGate: func(_ context.Context, _ string, _ json.RawMessage, _ []llm.Message) WatchdogDecision {
			return WatchdogDecision{Action: "challenge", Protocol: "debug-loop", Challenge: "no evidence"}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tool.executed {
		t.Fatal("challenge must skip tool execution")
	}
	var found bool
	for _, m := range result.History {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult && b.ToolUseRef == "u1" &&
				strings.Contains(b.Content, "watchdog") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("expected an injected tool-result mentioning 'watchdog'")
	}
}

// TestWatchdogGate_AllowExecutes: when the gate returns "allow", the tool runs
// exactly as it would with no gate (subject to the normal permission gate).
func TestWatchdogGate_AllowExecutes(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "edit_file",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockText, Text: "done"}},
		},
		caps: llm.Capabilities{SupportsTools: true},
	}
	tool := &fakeWTool{}
	reg := agenttools.NewRegistry()
	reg.MustRegister(tool)
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	_, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms, UserInput: "edit it",
		PermissionRequester: func(_ context.Context, _, _ string, _ json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
			return true, nil
		},
		WatchdogGate: func(_ context.Context, _ string, _ json.RawMessage, _ []llm.Message) WatchdogDecision {
			return WatchdogDecision{Action: "allow"}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !tool.executed {
		t.Fatal("allow must let the tool execute")
	}
}
