package compaction

import (
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func toolUse(id, name, input string) llm.Block {
	return llm.Block{Type: llm.BlockToolUse, ToolUseID: id, ToolName: name, ToolInput: []byte(input)}
}
func toolResult(ref, content string) llm.Block {
	return llm.Block{Type: llm.BlockToolResult, ToolUseRef: ref, Content: content}
}

func TestElide_StubsSupersededDuplicateReads(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{toolUse("u1", "read", `{"path":"a.go"}`)}},
		{Role: llm.RoleUser, Blocks: []llm.Block{toolResult("u1", "OLD CONTENTS of a.go")}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{toolUse("u2", "read", `{"path":"a.go"}`)}},
		{Role: llm.RoleUser, Blocks: []llm.Block{toolResult("u2", "NEW CONTENTS of a.go")}},
	}
	out, collapsed := ElideSupersededToolResults(msgs)
	if collapsed != 1 {
		t.Fatalf("expected 1 stubbed result, got %d", collapsed)
	}
	flat := ""
	for _, m := range out {
		for _, b := range m.Blocks {
			flat += b.Content
		}
	}
	if strings.Contains(flat, "OLD CONTENTS") {
		t.Error("superseded (older) result should be stubbed away")
	}
	if !strings.Contains(flat, "NEW CONTENTS") {
		t.Error("latest result must be kept verbatim")
	}
	// Pairing must remain valid (all blocks kept, just content rewritten).
	if !llm.IsValidPairing(out) {
		t.Error("elision must preserve pairing validity")
	}
}
