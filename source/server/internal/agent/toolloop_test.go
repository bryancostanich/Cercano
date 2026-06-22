package agent

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm"
)

type mockProvider struct {
	scripts [][]llm.Block
	caps    llm.Capabilities
	calls   int
}

func (m *mockProvider) Name() string                   { return "mock" }
func (m *mockProvider) Capabilities() llm.Capabilities { return m.caps }
func (m *mockProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	out := llm.ChatResponse{Blocks: m.scripts[m.calls]}
	m.calls++
	return out, nil
}
func (m *mockProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}

func TestToolLoop_PlainText_TerminatesImmediately(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{{
			{Type: llm.BlockText, Text: "Done."},
		}},
		caps: llm.Capabilities{SupportsTools: true},
	}
	reg := agenttools.DefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		ConvHistory: nil, UserInput: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 1 {
		t.Errorf("plain text turn should make 1 call, made %d", prov.calls)
	}
	if result.FinalText != "Done." {
		t.Errorf("final text: %q", result.FinalText)
	}
}

func TestToolLoop_SingleToolCall_FeedsResultAndContinues(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{
				{Type: llm.BlockText, Text: "Reading..."},
				{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "list_dir",
					ToolInput: json.RawMessage(`{"path":"."}`)},
			},
			{{Type: llm.BlockText, Text: "Got it."}},
		},
		caps: llm.Capabilities{SupportsTools: true},
	}
	reg := agenttools.DefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		UserInput: "list this dir",
	})
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 2 {
		t.Errorf("expected 2 calls (tool + continuation), got %d", prov.calls)
	}
	if result.FinalText != "Got it." {
		t.Errorf("final: %q", result.FinalText)
	}
}

func TestToolLoop_RTierRunsConcurrently(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{
				{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "list_dir",
					ToolInput: json.RawMessage(`{"path":"."}`)},
				{Type: llm.BlockToolUse, ToolUseID: "u2", ToolName: "list_dir",
					ToolInput: json.RawMessage(`{"path":"."}`)},
			},
			{{Type: llm.BlockText, Text: "done"}},
		},
		caps: llm.Capabilities{SupportsTools: true, SupportsParallelTools: true},
	}
	reg := agenttools.DefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms, UserInput: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "done" {
		t.Errorf("final: %q", result.FinalText)
	}

	var found1, found2 bool
	for _, m := range result.History {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult && b.ToolUseRef == "u1" {
				found1 = true
			}
			if b.Type == llm.BlockToolResult && b.ToolUseRef == "u2" {
				found2 = true
			}
		}
	}
	if !found1 || !found2 {
		t.Errorf("missing tool results: u1=%v u2=%v", found1, found2)
	}
}

func TestToolLoop_UserDeniesWTier_TerminatesTurn(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{{
			{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "write_file",
				ToolInput: json.RawMessage(`{"path":"/tmp/x","content":"x"}`)},
		}},
		caps: llm.Capabilities{SupportsTools: true},
	}
	reg := agenttools.DefaultRegistry()
	dir := t.TempDir()
	perms, _ := LoadPermissionStore(dir + "/perms.yaml")
	_ = perms.SetMode(ModeStrict)

	requester := func(ctx context.Context, name string, args json.RawMessage, tier llm.Permission) (bool, error) {
		return false, nil
	}

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		PermissionRequester: requester, UserInput: "write x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 1 {
		t.Errorf("denial should NOT cause another loop iteration; calls=%d", prov.calls)
	}
	last := result.History[len(result.History)-1]
	if last.Role != llm.RoleUser || len(last.Blocks) == 0 || !last.Blocks[0].IsError {
		t.Errorf("expected error tool_result, got %+v", last)
	}
}
