package server

import (
	"context"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/pkg/proto"
)

// The core fix: a conversation with a durable snapshot is served from that
// snapshot, with no full-history assembly.
func TestGetContextUsage_ServesDurableSnapshot(t *testing.T) {
	s, store := newServerWithStore(t)
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "c1", "/p", "m"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveContextUsage(ctx, conversation.ContextUsage{
		ConversationID:     "c1",
		TokensUsed:         52_000,
		RawTokens:          9_400_000,
		MessageTokens:      44_000,
		SystemTokens:       1_200,
		ToolSchemaTokens:   8_000,
		OutputReserve:      8_192,
		EstimatedRequest:   61_392,
		ContextWindow:      128_000,
		ContextWindowKnown: true,
		Model:              "gpt-5.5",
		Source:             "turn",
		ComputedAt:         time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := s.GetContextUsage(ctx, &proto.GetContextUsageRequest{ConversationId: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.GetEstimatedRequestTokens(); got != 61_392 {
		t.Errorf("EstimatedRequestTokens = %d, want 61392", got)
	}
	if got := resp.GetRawTokens(); got != 9_400_000 {
		t.Errorf("RawTokens = %d, want 9400000", got)
	}
	if got := resp.GetModelMax(); got != 128_000 {
		t.Errorf("ModelMax = %d, want 128000", got)
	}
	if got := resp.GetUsageSource(); got != "snapshot" {
		t.Errorf("UsageSource = %q, want \"snapshot\"", got)
	}
	if resp.GetUsageComputedAt() <= 0 {
		t.Error("expected a usage_computed_at timestamp")
	}
}

// A conversation with nothing stored at all is honestly unknown. This is the
// one case that legitimately reports no numbers.
func TestGetContextUsage_EmptyConversationReportsNone(t *testing.T) {
	s, store := newServerWithStore(t)
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "empty", "/p", "m"); err != nil {
		t.Fatal(err)
	}

	resp, err := s.GetContextUsage(ctx, &proto.GetContextUsageRequest{ConversationId: "empty"})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.GetUsageSource(); got != "none" {
		t.Errorf("UsageSource = %q, want \"none\" for a conversation with no turns", got)
	}
}

// Cold start: turns exist but no snapshot. The meter must still show a real
// number (the reported bug was a confident 0 here), and the computed estimate
// must be cached so later polls are a single row read.
func TestGetContextUsage_ColdStartWarmsRawEstimate(t *testing.T) {
	s, store := newServerWithStore(t)
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "cold", "/p", "m"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := store.Append(ctx, conversation.Turn{
			ConversationID: "cold",
			Role:           "user",
			Content:        "a reasonably long user message that estimates to several tokens",
		}); err != nil {
			t.Fatal(err)
		}
	}

	resp, err := s.GetContextUsage(ctx, &proto.GetContextUsageRequest{ConversationId: "cold"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetRawTokens() <= 0 {
		t.Errorf("cold start must report positive raw tokens, got %d", resp.GetRawTokens())
	}
	if resp.GetTokensUsed() <= 0 {
		t.Errorf("cold start must report positive tokens used, got %d", resp.GetTokensUsed())
	}
	if got := resp.GetUsageSource(); got != "raw_estimate" {
		t.Errorf("UsageSource = %q, want \"raw_estimate\" on cold start", got)
	}

	// The estimate should now be cached rather than recomputed each poll.
	snap, ok, err := store.GetContextUsage(ctx, "cold")
	if err != nil || !ok {
		t.Fatalf("cold-start estimate should have been cached: ok=%v err=%v", ok, err)
	}
	if snap.Source != "raw_estimate" || snap.RawTokens <= 0 {
		t.Errorf("cached cold-start snapshot looks wrong: %+v", snap)
	}
}

// Turns appended after a snapshot make it a lower bound, which the client needs
// to know so it can mark the meter rather than present it as current.
func TestGetContextUsage_MarksStaleSnapshot(t *testing.T) {
	s, store := newServerWithStore(t)
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "c1", "/p", "m"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveContextUsage(ctx, conversation.ContextUsage{
		ConversationID:   "c1",
		TokensUsed:       1_000,
		MessageTokens:    1_000,
		EstimatedRequest: 1_000,
		ContextWindow:    128_000,
		Source:           "turn",
		ComputedAt:       time.Now().Add(-1 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, conversation.Turn{
		ConversationID: "c1", Role: "user", Content: "newer than the snapshot",
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := s.GetContextUsage(ctx, &proto.GetContextUsageRequest{ConversationId: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetUsageStale() {
		t.Error("snapshot older than the newest turn should be marked stale")
	}
	if resp.GetTokensUsed() <= 0 {
		t.Error("a stale snapshot must still report its numbers, not zero")
	}
}

// Live in-process accounting is the most accurate source and must win.
func TestGetContextUsage_LiveAccountingBeatsSnapshot(t *testing.T) {
	s, store := newServerWithStore(t)
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "c1", "/p", "m"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveContextUsage(ctx, conversation.ContextUsage{
		ConversationID:   "c1",
		TokensUsed:       1_000,
		MessageTokens:    1_000,
		EstimatedRequest: 1_000,
		ContextWindow:    128_000,
		Source:           "compaction",
		ComputedAt:       time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	s.recordRequestAccounting("c1", requestAccountingSnapshot{
		MessageTokens:          38_137,
		EstimatedRequestTokens: 60_120,
		ContextWindow:          128_000,
		ContextWindowKnown:     true,
	})

	resp, err := s.GetContextUsage(ctx, &proto.GetContextUsageRequest{ConversationId: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.GetEstimatedRequestTokens(); got != 60_120 {
		t.Errorf("EstimatedRequestTokens = %d, want live value 60120", got)
	}
	if got := resp.GetUsageSource(); got != "live" {
		t.Errorf("UsageSource = %q, want \"live\"", got)
	}
}
