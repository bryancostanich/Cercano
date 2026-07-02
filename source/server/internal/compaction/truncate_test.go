package compaction

import (
	"strings"
	"testing"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

func TestTruncateOldestToFit(t *testing.T) {
	tok := contextmeter.Default()
	mk := func(role llm.Role, text string) llm.Message {
		return llm.Message{Role: role, Blocks: []llm.Block{{Type: llm.BlockText, Text: text}}}
	}
	big := strings.Repeat("x ", 4000) // ~4k tokens per message under the default tokenizer
	msgs := []llm.Message{
		mk(llm.RoleUser, big), mk(llm.RoleAssistant, big),
		mk(llm.RoleUser, big), mk(llm.RoleAssistant, big),
	}
	limit := TotalTokens(tok, msgs[2:]) + 10 // room for the last two only

	got, dropped := TruncateOldestToFit(msgs, tok, limit, 0)
	if dropped != 2 || len(got) != 2 {
		t.Fatalf("dropped=%d len=%d", dropped, len(got))
	}
	if TotalTokens(tok, got) > limit {
		t.Fatal("still over limit")
	}

	// preserveLeading=1 keeps the summary preamble even while dropping behind it.
	got2, _ := TruncateOldestToFit(msgs, tok, limit, 1)
	if got2[0].Blocks[0].Text != msgs[0].Blocks[0].Text {
		t.Fatal("leading message must be preserved")
	}

	// Already-fits input is returned unchanged.
	got3, dropped3 := TruncateOldestToFit(msgs, tok, 1<<30, 0)
	if dropped3 != 0 || len(got3) != len(msgs) {
		t.Fatal("must not touch a fitting view")
	}
}

func TestTruncateNeverLeadsWithToolResult(t *testing.T) {
	tok := contextmeter.Default()
	big := strings.Repeat("x ", 4000)
	msgs := []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: big}}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolName: "t", ToolInput: []byte(`{}`)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, Content: big}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "tail"}}},
	}
	// Force a limit that lands the cut between the tool_use and its result.
	limit := TotalTokens(tok, msgs[2:]) + 5
	got, _ := TruncateOldestToFit(msgs, tok, limit, 0)
	if len(got) > 0 {
		for _, b := range got[0].Blocks {
			if b.Type == llm.BlockToolResult {
				t.Fatal("truncated view must not begin with an orphaned tool_result")
			}
		}
	}
}
