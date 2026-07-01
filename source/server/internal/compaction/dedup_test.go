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

func TestKeepLastN_StubsOldestPreservesNewest(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{toolUse("u1", "read", `{"path":"a.go"}`)}},
		{Role: llm.RoleUser, Blocks: []llm.Block{toolResult("u1", "AAAA")}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{toolUse("u2", "read", `{"path":"b.go"}`)}},
		{Role: llm.RoleUser, Blocks: []llm.Block{toolResult("u2", "BBBB")}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{toolUse("u3", "read", `{"path":"c.go"}`)}},
		{Role: llm.RoleUser, Blocks: []llm.Block{toolResult("u3", "CCCC")}},
	}
	// n=1: keep only the last tool_result verbatim; stub the two older ones.
	out, stubbed := KeepLastNToolResults(msgs, 1)
	if stubbed != 2 {
		t.Fatalf("expected 2 stubbed, got %d", stubbed)
	}
	flat := ""
	for _, m := range out {
		for _, b := range m.Blocks {
			flat += b.Content
		}
	}
	if strings.Contains(flat, "AAAA") || strings.Contains(flat, "BBBB") {
		t.Errorf("older tool_results should be stubbed: %s", flat)
	}
	if !strings.Contains(flat, "CCCC") {
		t.Errorf("newest tool_result must survive: %s", flat)
	}
	if !llm.IsValidPairing(out) {
		t.Error("elision must preserve pairing validity")
	}
	// n greater than the count: no-op.
	out2, stubbed2 := KeepLastNToolResults(msgs, 999)
	if stubbed2 != 0 {
		t.Errorf("n > count should stub 0, got %d", stubbed2)
	}
	if len(out2) != len(msgs) {
		t.Errorf("output length must match")
	}
	// n=0: stub everything.
	out3, stubbed3 := KeepLastNToolResults(msgs, 0)
	if stubbed3 != 3 {
		t.Errorf("n=0 should stub every tool_result, got %d", stubbed3)
	}
	flat3 := ""
	for _, m := range out3 {
		for _, b := range m.Blocks {
			flat3 += b.Content
		}
	}
	if strings.Contains(flat3, "AAAA") || strings.Contains(flat3, "BBBB") || strings.Contains(flat3, "CCCC") {
		t.Errorf("n=0 must stub every result: %s", flat3)
	}
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
