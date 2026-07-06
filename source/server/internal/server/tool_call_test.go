package server

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

func TestGetToolCall_FullArgsResult(t *testing.T) {
	srv, store := newServerWithStore(t)
	ctx := context.Background()
	convID := "conv-tool"
	if err := store.EnsureConversation(ctx, convID, "", "test-model"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	args := `{"path":"internal/ui","pattern":"foo"}`
	result := "line one\nline two\nline three"
	useJSON, _ := json.Marshal([]llm.Block{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "Grep", ToolInput: json.RawMessage(args)}})
	resJSON, _ := json.Marshal([]llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "u1", Content: result}})
	for _, tn := range []conversation.Turn{
		{ConversationID: convID, Role: "user", Content: "search please"},
		{ConversationID: convID, Role: "assistant", BlocksJSON: string(useJSON)},
		{ConversationID: convID, Role: "user", BlocksJSON: string(resJSON)},
	} {
		if err := store.Append(ctx, tn); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	resp, err := srv.GetToolCall(ctx, &proto.GetToolCallRequest{ConversationId: convID, ToolUseId: "u1"})
	if err != nil {
		t.Fatalf("GetToolCall: %v", err)
	}
	if !resp.Found {
		t.Fatal("expected found=true for a recorded tool call")
	}
	if resp.ToolName != "Grep" {
		t.Errorf("tool_name = %q, want Grep", resp.ToolName)
	}
	if resp.ArgsJson != args {
		t.Errorf("args_json = %q, want %q", resp.ArgsJson, args)
	}
	if resp.Result != result {
		t.Errorf("result = %q, want %q", resp.Result, result)
	}
	if resp.IsError {
		t.Error("is_error should be false for a successful call")
	}

	// Unknown tool_use_id → not found, no error.
	miss, err := srv.GetToolCall(ctx, &proto.GetToolCallRequest{ConversationId: convID, ToolUseId: "nope"})
	if err != nil {
		t.Fatalf("GetToolCall(miss): %v", err)
	}
	if miss.Found {
		t.Error("unknown tool_use_id should be found=false")
	}
}

func TestGetToolCall_ErrorResultFlagged(t *testing.T) {
	srv, store := newServerWithStore(t)
	ctx := context.Background()
	convID := "conv-tool-err"
	if err := store.EnsureConversation(ctx, convID, "", "m"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	useJSON, _ := json.Marshal([]llm.Block{{Type: llm.BlockToolUse, ToolUseID: "e1", ToolName: "Bash", ToolInput: json.RawMessage(`{"cmd":"false"}`)}})
	resJSON, _ := json.Marshal([]llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "e1", Content: "exit status 1", IsError: true}})
	for _, tn := range []conversation.Turn{
		{ConversationID: convID, Role: "assistant", BlocksJSON: string(useJSON)},
		{ConversationID: convID, Role: "user", BlocksJSON: string(resJSON)},
	} {
		if err := store.Append(ctx, tn); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	resp, err := srv.GetToolCall(ctx, &proto.GetToolCallRequest{ConversationId: convID, ToolUseId: "e1"})
	if err != nil {
		t.Fatalf("GetToolCall: %v", err)
	}
	if !resp.Found || !resp.IsError || resp.Result != "exit status 1" {
		t.Errorf("error result not surfaced correctly: %+v", resp)
	}
}
