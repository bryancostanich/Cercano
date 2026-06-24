package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

func TestGetConversationTurns_SummariesAndSideEffectFree(t *testing.T) {
	srv, store := newServerWithStore(t)
	ctx := context.Background()
	convID := "conv-view"
	if err := store.EnsureConversation(ctx, convID, "", "test-model"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	useJSON, _ := json.Marshal([]llm.Block{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "LS", ToolInput: json.RawMessage(`{"path":"."}`)}})
	resJSON, _ := json.Marshal([]llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "u1", Content: "a.go\nb.go"}})
	for _, tn := range []conversation.Turn{
		{ConversationID: convID, Role: "user", Content: "list the files please"},
		{ConversationID: convID, Role: "assistant", BlocksJSON: string(useJSON)},
		{ConversationID: convID, Role: "user", BlocksJSON: string(resJSON)},
	} {
		if err := store.Append(ctx, tn); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	resp, err := srv.GetConversationTurns(ctx, &proto.GetConversationTurnsRequest{ConversationId: convID})
	if err != nil {
		t.Fatalf("GetConversationTurns: %v", err)
	}
	if len(resp.Turns) != 3 {
		t.Fatalf("turns = %d, want 3", len(resp.Turns))
	}
	if resp.Turns[0].Role != "user" || resp.Turns[0].Kind != "text" || resp.Turns[0].Preview == "" || resp.Turns[0].EstTokens <= 0 {
		t.Errorf("turn0 = %+v", resp.Turns[0])
	}
	if resp.Turns[1].Kind != "tool_use" || resp.Turns[1].Preview == "" {
		t.Errorf("turn1 kind/preview = %+v", resp.Turns[1])
	}
	if resp.Turns[2].Kind != "tool_result" {
		t.Errorf("turn2 kind = %q", resp.Turns[2].Kind)
	}

	// Side-effect-free: usage must be unchanged (still zero — no turn was run).
	used, _ := srv.agent.GetContextUsage(ctx, convID)
	if used != 0 {
		t.Errorf("GetConversationTurns mutated the meter: used = %d, want 0", used)
	}
}

func TestCtTruncate_RuneBoundary(t *testing.T) {
	// 60 CJK runes = 180 bytes; truncating at 121 bytes splits a rune.
	// A correct implementation must back up to a rune boundary.
	s := strings.Repeat("世", 60)
	got := ctTruncate(s, 121)
	if !utf8.ValidString(got) {
		t.Fatalf("ctTruncate produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}
}
