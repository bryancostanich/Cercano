package conversation

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestStore_RoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Create + append two turns.
	if err := s.EnsureConversation(ctx, "conv1", "/tmp/proj", "qwen3-coder"); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ctx, Turn{ConversationID: "conv1", Role: "user", Content: "what is the capital of France?"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ctx, Turn{ConversationID: "conv1", Role: "assistant", Content: "Paris.", TokensIn: 12, TokensOut: 4}); err != nil {
		t.Fatal(err)
	}

	infos, err := s.List(ctx, "/tmp/proj", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("want 1 conversation, got %d", len(infos))
	}
	if infos[0].TurnCount != 2 {
		t.Errorf("turn count: want 2 got %d", infos[0].TurnCount)
	}
	if infos[0].Title != "what is the capital of France?" {
		t.Errorf("title not derived from first user turn: %q", infos[0].Title)
	}

	turns, err := s.GetTurns(ctx, "conv1")
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("want 2 turns, got %d", len(turns))
	}
	if turns[0].Content != "what is the capital of France?" || turns[1].Content != "Paris." {
		t.Errorf("turn ordering wrong: %+v", turns)
	}
}

func TestStore_ListSortedByRecency(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for i, id := range []string{"a", "b", "c"} {
		if err := s.EnsureConversation(ctx, id, "/p", "m"); err != nil {
			t.Fatal(err)
		}
		// Stagger last_turn_at via the turn's created_at.
		when := time.Unix(int64(1000+i*100), 0)
		if err := s.Append(ctx, Turn{ConversationID: id, Role: "user", Content: "hello", CreatedAt: when}); err != nil {
			t.Fatal(err)
		}
	}
	infos, err := s.List(ctx, "/p", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 3 || infos[0].ID != "c" || infos[2].ID != "a" {
		t.Errorf("want c,b,a got %v", []string{infos[0].ID, infos[1].ID, infos[2].ID})
	}
}

func TestStore_ListLimit(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, id := range []string{"a", "b", "c"} {
		_ = s.EnsureConversation(ctx, id, "/p", "m")
		_ = s.Append(ctx, Turn{ConversationID: id, Role: "user", Content: "hi"})
	}
	infos, err := s.List(ctx, "/p", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Errorf("limit ignored: got %d", len(infos))
	}
}

func TestStore_ProjectFilter(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_ = s.EnsureConversation(ctx, "a", "/proj1", "m")
	_ = s.Append(ctx, Turn{ConversationID: "a", Role: "user", Content: "hi"})
	_ = s.EnsureConversation(ctx, "b", "/proj2", "m")
	_ = s.Append(ctx, Turn{ConversationID: "b", Role: "user", Content: "hi"})

	infos, _ := s.List(ctx, "/proj1", 0)
	if len(infos) != 1 || infos[0].ID != "a" {
		t.Errorf("project filter broken: %+v", infos)
	}
	infos, _ = s.List(ctx, "", 0)
	if len(infos) != 2 {
		t.Errorf("empty filter should return all, got %d", len(infos))
	}
}

func TestStore_Delete(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_ = s.EnsureConversation(ctx, "a", "/p", "m")
	_ = s.Append(ctx, Turn{ConversationID: "a", Role: "user", Content: "hi"})
	if err := s.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	infos, _ := s.List(ctx, "", 0)
	if len(infos) != 0 {
		t.Errorf("delete didn't remove conversation: %+v", infos)
	}
	// FK cascade should remove turns too.
	turns, _ := s.GetTurns(ctx, "a")
	if len(turns) != 0 {
		t.Errorf("cascade delete didn't remove turns: %+v", turns)
	}
}

func TestStore_Rename_OverridesAutoDerived(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_ = s.EnsureConversation(ctx, "c1", "/p", "m")
	_ = s.Append(ctx, Turn{ConversationID: "c1", Role: "user", Content: "what's the capital of France?"})

	// Auto-derived title.
	infos, _ := s.List(ctx, "", 0)
	if infos[0].Title != "what's the capital of France?" {
		t.Fatalf("expected auto-derived title, got %q", infos[0].Title)
	}

	// Rename overrides.
	if err := s.Rename(ctx, "c1", "France geography"); err != nil {
		t.Fatal(err)
	}
	infos, _ = s.List(ctx, "", 0)
	if infos[0].Title != "France geography" {
		t.Errorf("expected renamed title, got %q", infos[0].Title)
	}

	// Subsequent turn must NOT overwrite the user-set title.
	_ = s.Append(ctx, Turn{ConversationID: "c1", Role: "user", Content: "and Germany?"})
	infos, _ = s.List(ctx, "", 0)
	if infos[0].Title != "France geography" {
		t.Errorf("rename clobbered by later turn: %q", infos[0].Title)
	}
}

func TestStore_Rename_EmptyTitleClears(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_ = s.EnsureConversation(ctx, "c1", "", "")
	_ = s.Append(ctx, Turn{ConversationID: "c1", Role: "user", Content: "hi"})
	if err := s.Rename(ctx, "c1", ""); err != nil {
		t.Fatal(err)
	}
	infos, _ := s.List(ctx, "", 0)
	if infos[0].Title != "" {
		t.Errorf("expected empty title after clear, got %q", infos[0].Title)
	}
}

func TestStore_AppendWithBlocksJSON_RoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.EnsureConversation(ctx, "c1", "/tmp", "claude"); err != nil {
		t.Fatal(err)
	}
	blocks := `[{"type":"tool_use","id":"u1","name":"read_file","input":{"path":"main.go"}}]`
	if err := s.Append(ctx, Turn{
		ID: "t1", ConversationID: "c1", Role: "assistant",
		Content: "", BlocksJSON: blocks, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	turns, err := s.GetTurns(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].BlocksJSON != blocks {
		t.Errorf("blocks not round-tripped: %+v", turns)
	}
}

func TestDeriveTitle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hello world", "hello world"},
		{"  trim me  ", "trim me"},
		{"/clear", ""},
		{"a very long prompt that should be truncated at the right place because it exceeds sixty characters easily", "a very long prompt that should be truncated at the right…"},
	}
	for _, c := range cases {
		got := DeriveTitle(c.in)
		if got != c.want {
			t.Errorf("DeriveTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUpdateRecapAndGet(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	if err := s.EnsureConversation(ctx, "c1", "/proj", "qwen3-coder"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRecap(ctx, "c1", "Refactored the router and added tests"); err != nil {
		t.Fatalf("UpdateRecap: %v", err)
	}

	got, err := s.Get(ctx, "c1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Recap != "Refactored the router and added tests" {
		t.Errorf("recap = %q", got.Recap)
	}
	if got.RecapUpdatedAt.IsZero() {
		t.Error("RecapUpdatedAt not set")
	}
}

func TestGetMissingReturnsError(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	if _, err := s.Get(context.Background(), "nope"); err == nil {
		t.Error("expected error for missing conversation")
	}
}

func TestListIncludesRecap(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	ctx := context.Background()
	_ = s.EnsureConversation(ctx, "c1", "", "")
	_ = s.UpdateRecap(ctx, "c1", "did a thing")
	list, err := s.List(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Recap != "did a thing" {
		t.Errorf("list recap = %+v", list)
	}
}

func TestGetTurns_SameSecondPreservesInsertionOrder(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.EnsureConversation(ctx, "c1", "", "m"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	ts := time.Unix(1_700_000_000, 0) // identical timestamp for all three
	for _, c := range []string{"a", "b", "c"} {
		if err := s.Append(ctx, Turn{ConversationID: "c1", Role: "assistant", Content: c, CreatedAt: ts}); err != nil {
			t.Fatalf("Append %s: %v", c, err)
		}
	}
	turns, err := s.GetTurns(ctx, "c1")
	if err != nil {
		t.Fatalf("GetTurns: %v", err)
	}
	got := []string{}
	for _, tn := range turns {
		got = append(got, tn.Content)
	}
	if strings.Join(got, "") != "abc" {
		t.Fatalf("order not preserved: got %v, want [a b c]", got)
	}
}

func TestDeleteTurns_RemovesOnlyNamed(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.EnsureConversation(ctx, "c1", "", "m"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := s.EnsureConversation(ctx, "c2", "", "m"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	// c1: three turns with known ids; c2: one turn that must survive.
	for _, tn := range []Turn{
		{ID: "a", ConversationID: "c1", Role: "user", Content: "one"},
		{ID: "b", ConversationID: "c1", Role: "assistant", Content: "two"},
		{ID: "c", ConversationID: "c1", Role: "user", Content: "three"},
		{ID: "z", ConversationID: "c2", Role: "user", Content: "other"},
	} {
		if err := s.Append(ctx, tn); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	if err := s.DeleteTurns(ctx, "c1", []string{"a", "c", "ghost"}); err != nil {
		t.Fatalf("DeleteTurns: %v", err)
	}
	got, _ := s.GetTurns(ctx, "c1")
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("c1 turns = %+v, want only [b]", got)
	}
	other, _ := s.GetTurns(ctx, "c2")
	if len(other) != 1 {
		t.Errorf("c2 should be untouched, got %d turns", len(other))
	}
	// Idempotent: deleting already-gone ids is a no-op, no error.
	if err := s.DeleteTurns(ctx, "c1", []string{"a"}); err != nil {
		t.Errorf("idempotent delete errored: %v", err)
	}
	// Empty id list is a no-op.
	if err := s.DeleteTurns(ctx, "c1", nil); err != nil {
		t.Errorf("empty delete errored: %v", err)
	}
}

func TestSetGeneratedTitle_SetsAutoTitle(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.EnsureConversation(ctx, "c1", "/proj", "m"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGeneratedTitle(ctx, "c1", "Fix The Scrollbar"); err != nil {
		t.Fatal(err)
	}
	info, err := s.Get(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "Fix The Scrollbar" {
		t.Errorf("title = %q, want %q", info.Title, "Fix The Scrollbar")
	}
}

func TestSetGeneratedTitle_NeverOverwritesUserRename(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.EnsureConversation(ctx, "c1", "/proj", "m"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(ctx, "c1", "My Title"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGeneratedTitle(ctx, "c1", "Generated Title"); err != nil {
		t.Fatal(err)
	}
	info, _ := s.Get(ctx, "c1")
	if info.Title != "My Title" {
		t.Errorf("user title was overwritten: got %q, want %q", info.Title, "My Title")
	}
}

// TestRename_BeforeEnsure_CreatesRow guards the race where /rename fires
// before EnsureConversation has inserted the row (e.g., a fresh session where
// the user renames before sending the first prompt). Pre-fix the SQL was a
// plain UPDATE; it affected 0 rows and returned nil, the CLI showed a false
// "renamed to: …" message, and the rename evaporated when EnsureConversation
// later inserted the row fresh. Upsert reserves the row up-front.
func TestRename_BeforeEnsure_CreatesRow(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.Rename(ctx, "fresh", "User Title"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	info, err := s.Get(ctx, "fresh")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if info.Title != "User Title" {
		t.Errorf("title = %q, want %q", info.Title, "User Title")
	}
}

// TestRename_BeforeEnsure_SurvivesFullPipeline reproduces the original bug
// end-to-end: Rename → EnsureConversation (which now backfills project/model
// on conflict) → Append first user turn (auto-derive guard must skip because
// title is non-empty) → SetGeneratedTitle (its title_source != 'user' guard
// must skip). After all of that the user's title still wins and the project
// context that arrived later is filled in.
func TestRename_BeforeEnsure_SurvivesFullPipeline(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.Rename(ctx, "fresh", "User Title"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := s.EnsureConversation(ctx, "fresh", "/some/proj", "qwen3-coder"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := s.Append(ctx, Turn{ConversationID: "fresh", Role: "user", Content: "what's a goroutine?"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.SetGeneratedTitle(ctx, "fresh", "AI Generated Title"); err != nil {
		t.Fatalf("set generated: %v", err)
	}
	info, err := s.Get(ctx, "fresh")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if info.Title != "User Title" {
		t.Errorf("user title lost after full pipeline: got %q, want %q", info.Title, "User Title")
	}
	if info.ProjectDir != "/some/proj" {
		t.Errorf("project_dir not backfilled: got %q, want %q", info.ProjectDir, "/some/proj")
	}
	if info.Model != "qwen3-coder" {
		t.Errorf("model not backfilled: got %q, want %q", info.Model, "qwen3-coder")
	}
}

// TestEnsureConversation_DoesNotOverwriteExistingProjectAndModel makes sure
// the new ON CONFLICT branch only fills *empty* project_dir/model. A second
// EnsureConversation from a re-attached agent (or a concurrent session) must
// not silently rebrand an existing conversation.
func TestEnsureConversation_DoesNotOverwriteExistingProjectAndModel(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.EnsureConversation(ctx, "c1", "/orig", "model-A"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureConversation(ctx, "c1", "/different", "model-B"); err != nil {
		t.Fatal(err)
	}
	info, _ := s.Get(ctx, "c1")
	if info.ProjectDir != "/orig" || info.Model != "model-A" {
		t.Errorf("re-ensure overwrote project/model: got (%q, %q), want (/orig, model-A)",
			info.ProjectDir, info.Model)
	}
}

func TestTurnPagingTailAndPages(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	convID := "conv-paging"
	if err := s.EnsureConversation(ctx, convID, "/tmp/project", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	base := time.Unix(1000, 0)
	for i := 0; i < 7; i++ {
		if err := s.Append(ctx, Turn{ID: fmt.Sprintf("t%d", i), ConversationID: convID, Role: "assistant", Content: fmt.Sprintf("turn-%d", i), CreatedAt: base.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	total, err := s.CountTurns(ctx, convID)
	if err != nil {
		t.Fatalf("CountTurns: %v", err)
	}
	if total != 7 {
		t.Fatalf("CountTurns = %d, want 7", total)
	}

	page, err := s.GetTurnPage(ctx, convID, 2, 3)
	if err != nil {
		t.Fatalf("GetTurnPage: %v", err)
	}
	assertTurnContents(t, page, []string{"turn-2", "turn-3", "turn-4"})

	tail, start, total, err := s.GetTailTurns(ctx, convID, 3)
	if err != nil {
		t.Fatalf("GetTailTurns: %v", err)
	}
	if start != 4 || total != 7 {
		t.Fatalf("tail start,total = %d,%d; want 4,7", start, total)
	}
	assertTurnContents(t, tail, []string{"turn-4", "turn-5", "turn-6"})
}

func TestTurnPagingBoundariesAndStableSameSecondOrder(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	convID := "conv-paging-boundaries"
	if err := s.EnsureConversation(ctx, convID, "/tmp/project", "model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}
	sameSecond := time.Unix(2000, 0)
	for i := 0; i < 4; i++ {
		if err := s.Append(ctx, Turn{ID: fmt.Sprintf("same-%d", i), ConversationID: convID, Role: "assistant", Content: fmt.Sprintf("same-%d", i), CreatedAt: sameSecond}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	oversized, start, total, err := s.GetTailTurns(ctx, convID, 99)
	if err != nil {
		t.Fatalf("GetTailTurns oversized: %v", err)
	}
	if start != 0 || total != 4 {
		t.Fatalf("oversized tail start,total = %d,%d; want 0,4", start, total)
	}
	assertTurnContents(t, oversized, []string{"same-0", "same-1", "same-2", "same-3"})

	beyond, err := s.GetTurnPage(ctx, convID, 99, 10)
	if err != nil {
		t.Fatalf("GetTurnPage beyond: %v", err)
	}
	if len(beyond) != 0 {
		t.Fatalf("GetTurnPage beyond len = %d, want 0", len(beyond))
	}

	emptyTail, start, total, err := s.GetTailTurns(ctx, "missing", 10)
	if err != nil {
		t.Fatalf("GetTailTurns missing: %v", err)
	}
	if len(emptyTail) != 0 || start != 0 || total != 0 {
		t.Fatalf("missing tail len,start,total = %d,%d,%d; want 0,0,0", len(emptyTail), start, total)
	}
}

func assertTurnContents(t *testing.T, turns []Turn, want []string) {
	t.Helper()
	if len(turns) != len(want) {
		t.Fatalf("len(turns) = %d, want %d", len(turns), len(want))
	}
	for i := range turns {
		if turns[i].Content != want[i] {
			t.Fatalf("turn[%d].Content = %q, want %q", i, turns[i].Content, want[i])
		}
	}
}
