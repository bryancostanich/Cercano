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
