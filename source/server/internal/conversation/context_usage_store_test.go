package conversation

import (
	"context"
	"testing"
	"time"
)

func TestContextUsage_MissingSnapshotReportsNotOK(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.EnsureConversation(ctx, "c1", "/p", "m"); err != nil {
		t.Fatal(err)
	}

	// The distinction that matters: "no snapshot yet" must be reported as
	// unknown (ok=false), not as a real zero-token reading.
	got, ok, err := s.GetContextUsage(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("expected ok=false for a conversation with no snapshot, got %+v", got)
	}
}

func TestContextUsage_RoundTripAndUpsert(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.EnsureConversation(ctx, "c1", "/p", "m"); err != nil {
		t.Fatal(err)
	}

	computed := time.Unix(1_700_000_000, 0)
	in := ContextUsage{
		ConversationID:     "c1",
		TokensUsed:         12_000,
		RawTokens:          980_000,
		MessageTokens:      11_000,
		SystemTokens:       600,
		ToolSchemaTokens:   400,
		OutputReserve:      4_096,
		EstimatedRequest:   16_096,
		ContextWindow:      200_000,
		ContextWindowKnown: true,
		Model:              "claude-sonnet",
		Source:             "turn",
		ComputedAt:         computed,
	}
	if err := s.SaveContextUsage(ctx, in); err != nil {
		t.Fatal(err)
	}

	got, ok, err := s.GetContextUsage(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true after saving a snapshot")
	}
	if got.TokensUsed != in.TokensUsed || got.RawTokens != in.RawTokens ||
		got.MessageTokens != in.MessageTokens || got.SystemTokens != in.SystemTokens ||
		got.ToolSchemaTokens != in.ToolSchemaTokens || got.OutputReserve != in.OutputReserve ||
		got.EstimatedRequest != in.EstimatedRequest || got.ContextWindow != in.ContextWindow ||
		!got.ContextWindowKnown || got.Model != in.Model || got.Source != in.Source {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if !got.ComputedAt.Equal(computed) {
		t.Fatalf("ComputedAt round-trip mismatch: got %s want %s", got.ComputedAt, computed)
	}

	// Upsert overwrites in place: one current snapshot per conversation.
	in.TokensUsed = 20_000
	in.Source = "compaction"
	if err := s.SaveContextUsage(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.GetContextUsage(ctx, "c1")
	if got.TokensUsed != 20_000 || got.Source != "compaction" {
		t.Fatalf("upsert should overwrite, got tokens=%d source=%q", got.TokensUsed, got.Source)
	}
}

func TestContextUsage_DefaultsComputedAtWhenUnset(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.EnsureConversation(ctx, "c1", "/p", "m"); err != nil {
		t.Fatal(err)
	}

	before := time.Now().Add(-2 * time.Second)
	if err := s.SaveContextUsage(ctx, ContextUsage{ConversationID: "c1", TokensUsed: 5, Source: "turn"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetContextUsage(ctx, "c1")
	if err != nil || !ok {
		t.Fatalf("get failed: ok=%v err=%v", ok, err)
	}
	if got.ComputedAt.Before(before) {
		t.Fatalf("expected ComputedAt to default to now, got %s", got.ComputedAt)
	}
}

func TestContextUsage_RequiresConversationID(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, _, err := s.GetContextUsage(ctx, ""); err == nil {
		t.Error("expected error getting usage with empty conversation id")
	}
	if err := s.SaveContextUsage(ctx, ContextUsage{}); err == nil {
		t.Error("expected error saving usage with empty conversation id")
	}
}
