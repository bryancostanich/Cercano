package compactor

import (
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAdvanceDoesNotFreezeCallWhoseResultIsRecent(t *testing.T) {
	makeTurn := func(sec int64, role llm.Role, blocks ...llm.Block) conversation.Turn {
		b, _ := json.Marshal(blocks)
		return conversation.Turn{Role: string(role), BlocksJSON: string(b), CreatedAt: time.Unix(sec, 0)}
	}
	turns := []conversation.Turn{
		makeTurn(1, llm.RoleUser, llm.Block{Type: llm.BlockText, Text: strings.Repeat("task ", 1000)}),
		makeTurn(2, llm.RoleAssistant, llm.Block{Type: llm.BlockText, Text: strings.Repeat("investigating ", 1000)}, llm.Block{Type: llm.BlockToolUse, ToolUseID: "call", ToolName: "Grep", ToolInput: []byte(`{}`)}),
		makeTurn(3, llm.RoleUser, llm.Block{Type: llm.BlockToolResult, ToolUseRef: "call", Content: "FOUND"}),
		makeTurn(4, llm.RoleUser, llm.Block{Type: llm.BlockText, Text: "next"}),
	}
	summarize := func(_ context.Context, msgs []llm.Message) (compaction.StructuredSummary, error) {
		return compaction.StructuredSummary{Goal: "task"}, nil
	}
	st, changed, _, err := Advance(context.Background(), turns, conversation.Compaction{}, summarize, Config{ActivationFloorTokens: 1, SegmentTokens: 10, VerbatimRecent: 2}, contextmeter.Default())
	if err != nil {
		t.Fatal(err)
	}
	if !changed || st.FrozenThrough != 1 {
		t.Fatalf("froze part of tool exchange: changed=%v boundary=%d", changed, st.FrozenThrough)
	}
	view, err := BuildSendView(turns, st)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range view {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult && b.Content == "FOUND" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("lost recent evidence")
	}
}
