package llm

import "testing"

func TestRepairPairing_DropsOrphanToolUse(t *testing.T) {
	msgs := []Message{
		{Role: RoleAssistant, Blocks: []Block{{Type: BlockToolUse, ToolUseID: "t1", ToolName: "read"}}},
		// no tool_result for t1
	}
	got := RepairPairing(msgs)
	if len(got) != 0 {
		t.Fatalf("orphan tool_use should be dropped, got %d messages", len(got))
	}
	if IsValidPairing(msgs) {
		t.Error("input with orphan tool_use should be invalid")
	}
}

func TestRepairPairing_KeepsValidPair(t *testing.T) {
	msgs := []Message{
		{Role: RoleAssistant, Blocks: []Block{{Type: BlockToolUse, ToolUseID: "t1"}}},
		{Role: RoleUser, Blocks: []Block{{Type: BlockToolResult, ToolUseRef: "t1", Content: "ok"}}},
	}
	if !IsValidPairing(msgs) {
		t.Error("a use followed by its result is valid")
	}
	if got := RepairPairing(msgs); len(got) != 2 {
		t.Fatalf("valid pair must survive, got %d", len(got))
	}
}
