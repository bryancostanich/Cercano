package conversation

import (
	"context"
	"testing"
)

// Opening the same DB twice must be a no-op the second time: the precursor_id
// migration (like every ADD COLUMN in the list) has to be idempotent.
func TestRollover_MigrationIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/conv.db"
	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	s1.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second open (migration not idempotent?): %v", err)
	}
	s2.Close()
}

// CreateRolledOver mints a new 'main' conversation linked to its precursor and
// seeds exactly the handoff turn.
func TestRollover_CreateAndPrecursor(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.EnsureConversation(ctx, "old", "/p", "m"); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ctx, Turn{ConversationID: "old", Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}

	handoff := Turn{Role: "user", Content: "[handoff] here is where we left off"}
	if err := s.CreateRolledOver(ctx, "new", "/p", "m", "old", handoff); err != nil {
		t.Fatal(err)
	}

	// The new conversation exists, is 'main', and links back to "old".
	info, err := s.Get(ctx, "new")
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != "main" {
		t.Fatalf("rolled-over conversation should be kind 'main', got %q", info.Kind)
	}
	if info.PrecursorID != "old" {
		t.Fatalf("precursor_id = %q, want %q", info.PrecursorID, "old")
	}

	// Exactly one turn — the handoff — is present.
	turns, err := s.GetTurns(ctx, "new")
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("new conversation should have exactly 1 seeded turn, got %d", len(turns))
	}
	if turns[0].Content != handoff.Content {
		t.Fatalf("seeded turn content = %q, want %q", turns[0].Content, handoff.Content)
	}

	// The predecessor is untouched.
	oldTurns, err := s.GetTurns(ctx, "old")
	if err != nil {
		t.Fatal(err)
	}
	if len(oldTurns) != 1 || oldTurns[0].Content != "hello" {
		t.Fatalf("predecessor must be untouched, got %d turns", len(oldTurns))
	}
}

// Precursor returns "" for a conversation that was not created by a rollover,
// and the linked id for one that was.
func TestRollover_PrecursorEmptyForRoot(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.EnsureConversation(ctx, "root", "/p", "m"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Precursor(ctx, "root")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("root conversation precursor = %q, want empty", got)
	}
}

// A 3-deep chain A<-B<-C is walkable backward via Precursor().
func TestRollover_WalkLineage(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.EnsureConversation(ctx, "A", "/p", "m"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRolledOver(ctx, "B", "/p", "m", "A", Turn{Role: "user", Content: "h1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRolledOver(ctx, "C", "/p", "m", "B", Turn{Role: "user", Content: "h2"}); err != nil {
		t.Fatal(err)
	}

	// Walk C -> B -> A -> root.
	want := []string{"B", "A", ""}
	cur := "C"
	for i, exp := range want {
		p, err := s.Precursor(ctx, cur)
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if p != exp {
			t.Fatalf("step %d: precursor(%s) = %q, want %q", i, cur, p, exp)
		}
		cur = p
	}
}

// CreateRolledOver rejects a duplicate id rather than silently upserting.
func TestRollover_DuplicateIDRejected(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.EnsureConversation(ctx, "existing", "/p", "m"); err != nil {
		t.Fatal(err)
	}
	err = s.CreateRolledOver(ctx, "existing", "/p", "m", "some-precursor", Turn{Role: "user", Content: "h"})
	if err == nil {
		t.Fatal("expected CreateRolledOver to reject an existing id")
	}
}
