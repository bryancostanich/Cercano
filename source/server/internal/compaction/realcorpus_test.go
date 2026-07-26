package compaction

import (
	"testing"

	"cercano/source/server/internal/llm"
)

// TestRealConversationLoads is a smoke test: the embedded fixture must parse and
// carry the full, ordered transcript. It guards against a corrupted or
// truncated regeneration of testdata/real_conversation.json.
func TestRealConversationLoads(t *testing.T) {
	msgs := RealConversation()
	if len(msgs) < 1500 {
		t.Fatalf("expected the full ~1898-turn transcript, got %d messages", len(msgs))
	}

	var text, toolUse, toolResult int
	for _, m := range msgs {
		if m.Role != llm.RoleUser && m.Role != llm.RoleAssistant && m.Role != llm.RoleSystem {
			t.Fatalf("unexpected role %q", m.Role)
		}
		for _, b := range m.Blocks {
			switch b.Type {
			case llm.BlockText:
				text++
			case llm.BlockToolUse:
				toolUse++
			case llm.BlockToolResult:
				toolResult++
			}
		}
	}
	if text == 0 || toolUse == 0 || toolResult == 0 {
		t.Fatalf("fixture missing block variety: text=%d toolUse=%d toolResult=%d", text, toolUse, toolResult)
	}
	t.Logf("loaded %d messages (text=%d toolUse=%d toolResult=%d)", len(msgs), text, toolUse, toolResult)
}
