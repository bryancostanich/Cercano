package server

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

func TestExportContext_RoundTripsToMessages(t *testing.T) {
	s, store := newServerWithStore(t)
	ctx := context.Background()
	_ = store.EnsureConversation(ctx, "c1", "/p", "m")
	_ = store.Append(ctx, conversation.Turn{ConversationID: "c1", Role: "user", Content: "hello world"})

	resp, err := s.ExportContext(ctx, &proto.ExportContextRequest{ConversationId: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	var msgs []llm.Message
	if err := json.Unmarshal([]byte(resp.GetJson()), &msgs); err != nil {
		t.Fatalf("unmarshal JSON: %v", err)
	}
	if len(msgs) == 0 {
		t.Error("expected at least one message in exported context")
	}
}

func TestGetCompactionState_NoStateIsEmpty(t *testing.T) {
	s, store := newServerWithStore(t)
	ctx := context.Background()
	_ = store.EnsureConversation(ctx, "c1", "/p", "m")
	resp, err := s.GetCompactionState(ctx, &proto.GetCompactionStateRequest{ConversationId: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetFrozenTurns() != 0 || resp.GetConsolidatedSummary() != "" {
		t.Errorf("no compaction → empty state, got %+v", resp)
	}
}

func TestGetContextUsage_RawIsCheapEstimateNotZero(t *testing.T) {
	s, store := newServerWithStore(t)
	ctx := context.Background()
	_ = store.EnsureConversation(ctx, "c1", "/p", "m")
	_ = store.Append(ctx, conversation.Turn{ConversationID: "c1", Role: "user",
		Content: "a reasonably long user message that should estimate to several tokens"})

	resp, err := s.GetContextUsage(ctx, &proto.GetContextUsageRequest{ConversationId: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetRawTokens() <= 0 {
		t.Errorf("raw_tokens should be a positive estimate, got %d", resp.GetRawTokens())
	}
	// No compaction state → sent == raw still holds (both derived from the same turns).
	if resp.GetTokensUsed() != resp.GetRawTokens() {
		t.Errorf("no compaction → sent==raw: sent=%d raw=%d", resp.GetTokensUsed(), resp.GetRawTokens())
	}
}
