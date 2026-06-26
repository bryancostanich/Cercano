package conversation

import (
	"context"
	"testing"
	"time"
)

func appendAt(t *testing.T, s Store, conv, id, content string, at int64) {
	if err := s.Append(context.Background(), Turn{
		ID: id, ConversationID: conv, Role: "user", Content: content,
		BlocksJSON: `[{"type":"text"}]`, CreatedAt: time.Unix(at, 0),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPruneRawBodies_OnlyFrozenAndOld(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	ctx := context.Background()
	_ = s.EnsureConversation(ctx, "c1", "/p", "m")
	appendAt(t, s, "c1", "old", "OLD BIG BODY", 100)    // frozen + old → stub
	appendAt(t, s, "c1", "recentFrozen", "KEEP-A", 250) // frozen but NOT old → keep
	appendAt(t, s, "c1", "live", "KEEP-B", 400)         // not frozen → keep

	// frozenThrough=300 (old+recentFrozen frozen), cutoff before=200 (only old).
	n, err := s.PruneRawBodies(ctx, "c1", 200, 300)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 pruned, got %d", n)
	}
	turns, _ := s.GetTurns(ctx, "c1")
	got := map[string]Turn{}
	for _, tn := range turns {
		got[tn.ID] = tn
	}
	if got["old"].Content != PrunedBodyStub || got["old"].BlocksJSON != "" {
		t.Errorf("old turn not stubbed: %+v", got["old"])
	}
	if got["recentFrozen"].Content != "KEEP-A" || got["live"].Content != "KEEP-B" {
		t.Error("recent/un-frozen turns must be kept verbatim")
	}
	// Idempotent: a second run prunes nothing.
	if n2, _ := s.PruneRawBodies(ctx, "c1", 200, 300); n2 != 0 {
		t.Errorf("second prune should be a no-op, got %d", n2)
	}
}

func TestCollapseConversation_KeepsIdentityDropsRest(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	ctx := context.Background()
	_ = s.EnsureConversation(ctx, "c1", "/p", "m")
	appendAt(t, s, "c1", "t1", "body", 100)
	_ = s.UpdateRecap(ctx, "c1", "the recap")
	_ = s.SaveCompaction(ctx, Compaction{ConversationID: "c1", FrozenThrough: 100, ConsolidatedJSON: `{"Goal":"g"}`})

	if err := s.CollapseConversation(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	turns, _ := s.GetTurns(ctx, "c1")
	if len(turns) != 0 {
		t.Errorf("collapse should delete all turns, got %d", len(turns))
	}
	comp, _ := s.GetCompaction(ctx, "c1")
	if comp.ConsolidatedJSON != "" {
		t.Error("collapse should delete the compaction row")
	}
	info, err := s.Get(ctx, "c1")
	if err != nil {
		t.Fatalf("conversation row must survive: %v", err)
	}
	if info.Recap != "the recap" {
		t.Errorf("identity recap must survive, got %q", info.Recap)
	}
	// Idempotent.
	if err := s.CollapseConversation(ctx, "c1"); err != nil {
		t.Errorf("second collapse should be a no-op, got %v", err)
	}
}
