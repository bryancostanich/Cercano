package conversation

import (
	"context"
	"testing"
)

func TestCompaction_RoundTrip(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.EnsureConversation(ctx, "c1", "/p", "m"); err != nil {
		t.Fatal(err)
	}

	// Missing row → zero value, no error.
	zero, err := s.GetCompaction(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if zero.FrozenThrough != 0 || zero.SegmentSummariesJSON != "" {
		t.Errorf("missing compaction should be zero value, got %+v", zero)
	}

	in := Compaction{
		ConversationID:       "c1",
		FrozenThrough:        1700,
		SegmentSummariesJSON: `[{"Goal":"g"}]`,
		ConsolidatedJSON:     `{"Goal":"g"}`,
		CompactedTokens:      4096,
	}
	if err := s.SaveCompaction(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetCompaction(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.FrozenThrough != 1700 || got.SegmentSummariesJSON != in.SegmentSummariesJSON ||
		got.ConsolidatedJSON != in.ConsolidatedJSON || got.CompactedTokens != 4096 {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// Upsert overwrites.
	in.FrozenThrough = 1800
	if err := s.SaveCompaction(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetCompaction(ctx, "c1")
	if got.FrozenThrough != 1800 {
		t.Errorf("upsert should overwrite, got FrozenThrough=%d", got.FrozenThrough)
	}
}
