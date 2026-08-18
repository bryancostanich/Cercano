package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// TestToolLoop_FollowUpDenial_ContinuesTurnWithMessage verifies the "[c] chat
// about this" path: when the PermissionRequester declines a tool call with a
// FollowUpDenial, the loop records the user's message as that call's tool_result
// and CONTINUES the turn (the model is called again to respond to the redirect),
// unlike a plain deny which ends the turn after one call.
func TestToolLoop_FollowUpDenial_ContinuesTurnWithMessage(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "Write",
				ToolInput: json.RawMessage(`{"path":"/tmp/x","content":"x"}`)}},
			{{Type: llm.BlockText, Text: "understood — summarizing instead"}},
		},
		caps: inference.Capabilities{SupportsTools: true},
	}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")
	_ = perms.SetMode(ModeStrict)

	const redirect = "don't write that — summarize the file instead"
	requester := func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission, destructive bool) (bool, error) {
		return false, &FollowUpDenial{Message: redirect}
	}

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		PermissionRequester: requester, UserInput: "write x",
	})
	if err != nil {
		t.Fatal(err)
	}

	// The redirect CONTINUES the turn: the provider is called a second time to
	// respond to it. A plain deny would stop at one call.
	if prov.calls != 2 {
		t.Errorf("follow-up denial should continue the turn; provider calls=%d, want 2", prov.calls)
	}

	// The redirect message is recorded verbatim as u1's tool_result (IsError,
	// because the tool did not execute).
	var got string
	var isErr, found bool
	for _, msg := range result.History {
		for _, b := range msg.Blocks {
			if b.Type == llm.BlockToolResult && b.ToolUseRef == "u1" {
				got, isErr, found = b.Content, b.IsError, true
			}
		}
	}
	if !found {
		t.Fatal("no tool_result recorded for u1")
	}
	if got != redirect {
		t.Errorf("tool_result content = %q, want the redirect %q", got, redirect)
	}
	if !isErr {
		t.Error("redirect tool_result should be marked IsError (the tool did not run)")
	}
}

func TestToolLoop_FollowUpDenial_SessionControlTerminatesTurn(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "request_autonomous_execution",
				ToolInput: json.RawMessage(`{"goal":"do the approved plan"}`)}},
			{{Type: llm.BlockToolUse, ToolUseID: "u2", ToolName: "Bash",
				ToolInput: json.RawMessage(`{"cmd":["echo","should-not-run"]}`)}},
		},
		caps: inference.Capabilities{SupportsTools: true},
	}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")
	_ = perms.SetMode(ModeStrict)

	const redirect = "exec plan mode and execute autonomously"
	requester := func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission, destructive bool) (bool, error) {
		return false, &FollowUpDenial{Message: redirect}
	}

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		PermissionRequester: requester, UserInput: "do it",
	})
	if err != nil {
		t.Fatal(err)
	}

	if prov.calls != 1 {
		t.Fatalf("session-control follow-up denial should terminate the turn; provider calls=%d, want 1", prov.calls)
	}
	if !resultHasToolResult(result, "u1", redirect, true) {
		t.Fatalf("expected redirect tool_result for request_autonomous_execution")
	}
	if resultHasToolResult(result, "u2", "", false) {
		t.Fatalf("unexpected second tool result; session-control redirect should stop before the next model/tool step")
	}
}

func TestToolLoop_SessionControlExecutionErrorTerminatesTurn(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "request_autonomous_execution",
				ToolInput: json.RawMessage(`{"goal":"do the approved plan"}`)}},
			{{Type: llm.BlockToolUse, ToolUseID: "u2", ToolName: "Bash",
				ToolInput: json.RawMessage(`{"cmd":["echo","should-not-run"]}`)}},
		},
		caps: inference.Capabilities{SupportsTools: true},
	}
	reg := testDefaultRegistry()

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, UserInput: "do it",
		PreauthorizedTools: []string{"request_autonomous_execution"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if prov.calls != 1 {
		t.Fatalf("session-control execution error should terminate the turn; provider calls=%d, want 1", prov.calls)
	}
	if !resultHasToolResultContaining(result, "u1", "autonomy ledger is not available", true) {
		t.Fatalf("expected execution error tool_result for request_autonomous_execution")
	}
	if resultHasToolResult(result, "u2", "", false) {
		t.Fatalf("unexpected second tool result; session-control error should stop before the next model/tool step")
	}
}

func resultHasToolResult(result ToolLoopResult, ref, content string, wantErr bool) bool {
	for _, msg := range result.History {
		for _, b := range msg.Blocks {
			if b.Type != llm.BlockToolResult || b.ToolUseRef != ref {
				continue
			}
			if content != "" && b.Content != content {
				continue
			}
			if b.IsError != wantErr {
				continue
			}
			return true
		}
	}
	return false
}

func resultHasToolResultContaining(result ToolLoopResult, ref, content string, wantErr bool) bool {
	for _, msg := range result.History {
		for _, b := range msg.Blocks {
			if b.Type == llm.BlockToolResult && b.ToolUseRef == ref && strings.Contains(b.Content, content) && b.IsError == wantErr {
				return true
			}
		}
	}
	return false
}
