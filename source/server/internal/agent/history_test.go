package agent

import (
	"encoding/json"
	"testing"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

func blocksJSON(t *testing.T, bs []llm.Block) string {
	t.Helper()
	b, err := json.Marshal(bs)
	if err != nil { t.Fatalf("marshal: %v", err) }
	return string(b)
}

func TestBuildLLMHistory_TextOnly(t *testing.T) {
	turns := []conversation.Turn{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "yo"},
	}
	got := BuildLLMHistory(turns)
	if len(got) != 2 { t.Fatalf("len = %d, want 2", len(got)) }
	if got[0].Role != llm.RoleUser || got[0].Blocks[0].Text != "hi" { t.Errorf("turn0 = %+v", got[0]) }
	if got[1].Role != llm.RoleAssistant || got[1].Blocks[0].Text != "yo" { t.Errorf("turn1 = %+v", got[1]) }
}

func TestBuildLLMHistory_ToolRoundTripPreserved(t *testing.T) {
	useBlocks := []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "LS", ToolInput: json.RawMessage(`{}`)}}
	resBlocks := []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "u1", Content: "ok"}}
	turns := []conversation.Turn{
		{Role: "user", Content: "list"},
		{Role: "assistant", BlocksJSON: blocksJSON(t, useBlocks)},
		{Role: "user", BlocksJSON: blocksJSON(t, resBlocks)},
		{Role: "assistant", Content: "done"},
	}
	got := BuildLLMHistory(turns)
	if len(got) != 4 { t.Fatalf("len = %d, want 4", len(got)) }
	if got[1].Blocks[0].Type != llm.BlockToolUse || got[1].Blocks[0].ToolUseID != "u1" { t.Errorf("tool_use lost: %+v", got[1]) }
	if got[2].Blocks[0].Type != llm.BlockToolResult || got[2].Blocks[0].ToolUseRef != "u1" { t.Errorf("tool_result lost: %+v", got[2]) }
}

func TestBuildLLMHistory_OrphanToolUseStripped(t *testing.T) {
	useBlocks := []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "LS"}}
	turns := []conversation.Turn{
		{Role: "user", Content: "list"},
		{Role: "assistant", BlocksJSON: blocksJSON(t, useBlocks)}, // no following tool_result (legacy lossy data)
	}
	got := BuildLLMHistory(turns)
	if len(got) != 1 { t.Fatalf("len = %d, want 1 (orphan tool_use message dropped)", len(got)) }
	if got[0].Blocks[0].Text != "list" { t.Errorf("kept wrong message: %+v", got[0]) }
}

func TestBuildLLMHistory_OrphanToolResultStripped(t *testing.T) {
	resBlocks := []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "ghost", Content: "x"}}
	turns := []conversation.Turn{
		{Role: "user", Content: "hi"},
		{Role: "user", BlocksJSON: blocksJSON(t, resBlocks)}, // no matching tool_use
	}
	got := BuildLLMHistory(turns)
	if len(got) != 1 { t.Fatalf("len = %d, want 1 (orphan tool_result dropped)", len(got)) }
	if got[0].Blocks[0].Text != "hi" { t.Errorf("kept wrong message: %+v", got[0]) }
}

func TestBuildLLMHistory_OutOfOrderToolResultDropped(t *testing.T) {
	resBlocks := []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "u1", Content: "x"}}
	useBlocks := []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "LS"}}
	turns := []conversation.Turn{
		{Role: "user", BlocksJSON: blocksJSON(t, resBlocks)},      // result BEFORE use (invalid order)
		{Role: "assistant", BlocksJSON: blocksJSON(t, useBlocks)}, // use AFTER result
	}
	got := BuildLLMHistory(turns)
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0 (out-of-order pair fully dropped): %+v", len(got), got)
	}
}

func TestBuildLLMHistory_MixedTextAndOrphanToolUse(t *testing.T) {
	mixed := []llm.Block{
		{Type: llm.BlockText, Text: "thinking"},
		{Type: llm.BlockToolUse, ToolUseID: "u9", ToolName: "LS"}, // orphan: no following tool_result
	}
	turns := []conversation.Turn{{Role: "assistant", BlocksJSON: blocksJSON(t, mixed)}}
	got := BuildLLMHistory(turns)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (message kept for its text block)", len(got))
	}
	if len(got[0].Blocks) != 1 || got[0].Blocks[0].Type != llm.BlockText || got[0].Blocks[0].Text != "thinking" {
		t.Errorf("expected only the text block to survive, got %+v", got[0].Blocks)
	}
}
