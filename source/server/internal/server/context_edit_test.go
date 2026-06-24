package server

import (
	"context"
	"testing"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/pkg/proto"
)

func TestDeleteConversationTurns_Deletes(t *testing.T) {
	srv, store := newServerWithStore(t)
	ctx := context.Background()
	_ = store.EnsureConversation(ctx, "c1", "", "m")
	for _, tn := range []conversation.Turn{
		{ID: "a", ConversationID: "c1", Role: "user", Content: "one"},
		{ID: "b", ConversationID: "c1", Role: "assistant", Content: "two"},
	} {
		_ = store.Append(ctx, tn)
	}
	resp, err := srv.DeleteConversationTurns(ctx, &proto.DeleteConversationTurnsRequest{
		ConversationId: "c1", TurnId: []string{"a"},
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if resp.Deleted != 1 {
		t.Errorf("deleted = %d, want 1", resp.Deleted)
	}
	got, _ := store.GetTurns(ctx, "c1")
	if len(got) != 1 || got[0].ID != "b" {
		t.Errorf("turns = %+v, want [b]", got)
	}
}
