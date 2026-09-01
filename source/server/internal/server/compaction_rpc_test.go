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

func TestGetContextUsage_PrefersLiveRequestAccounting(t *testing.T) {
	s, _ := newServerWithStore(t)
	s.recordRequestAccounting("live-fast", requestAccountingSnapshot{
		MessageTokens:          38137,
		SystemTokens:           1178,
		ToolSchemaTokens:       12613,
		OutputReserveTokens:    8192,
		EstimatedRequestTokens: 60120,
		ContextWindow:          128000,
		ContextWindowKnown:     true,
	})

	resp, err := s.GetContextUsage(context.Background(), &proto.GetContextUsageRequest{ConversationId: "live-fast"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resp.GetEstimatedRequestTokens(), int32(60120); got != want {
		t.Fatalf("EstimatedRequestTokens = %d, want %d", got, want)
	}
	if got, want := resp.GetTokensUsed(), int32(60120); got != want {
		t.Fatalf("TokensUsed = %d, want live request estimate %d", got, want)
	}
	if got, want := resp.GetMessageTokens(), int32(38137); got != want {
		t.Fatalf("MessageTokens = %d, want %d", got, want)
	}
	if got, want := resp.GetRawTokens(), int32(38137); got != want {
		t.Fatalf("RawTokens = %d, want conservative live message-token lower-bound %d", got, want)
	}
	if got, want := resp.GetModelMax(), int32(128000); got != want {
		t.Fatalf("ModelMax = %d, want %d", got, want)
	}
	if !resp.GetContextWindowKnown() {
		t.Fatal("ContextWindowKnown = false, want true")
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
	// Sent tokens now come from the shared provider-facing request assembler,
	// while raw_tokens remains the cheap storage-size estimate for the UI's raw
	// savings figure. They need not be equal even with no compaction state.
	if resp.GetTokensUsed() <= 0 {
		t.Errorf("tokens_used should be a positive assembled send-view count, got %d", resp.GetTokensUsed())
	}
}
