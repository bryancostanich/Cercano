package conversation

import (
	"path/filepath"
	"testing"

	"cercano/source/server/internal/chatroute"
)

func TestSessionModelPersistenceIsolationRolloverAndClear(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "conversations.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"pinned", "other"} {
		if err := s.EnsureConversation(ctx, id, "/tmp", ""); err != nil {
			t.Fatal(err)
		}
	}
	routes := s.(chatroute.Store)
	pin := chatroute.Route{Profile: "deepinfra", Model: "exact-model"}
	if err := routes.SetChatRoute(ctx, "pinned", &pin); err != nil {
		t.Fatal(err)
	}
	if got, err := routes.ChatRoute(ctx, "other"); err != nil || got != nil {
		t.Fatalf("cross talk: %+v %v", got, err)
	}
	if err := s.CreateRolledOver(ctx, "rolled", "/tmp", "", "pinned", Turn{Content: "handoff"}); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSubagentConversation(ctx, "child", "pinned", "/tmp", "", nil); err != nil {
		t.Fatal(err)
	}
	if got, err := routes.ChatRoute(ctx, "child"); err != nil || got != nil {
		t.Fatal("child inherited override", got, err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	routes = s.(chatroute.Store)
	for _, id := range []string{"pinned", "rolled"} {
		got, err := routes.ChatRoute(ctx, id)
		if err != nil || got == nil || *got != pin {
			t.Fatalf("resume lost %s override: %+v %v", id, got, err)
		}
	}
	if err := routes.SetChatRoute(ctx, "pinned", nil); err != nil {
		t.Fatal(err)
	}
	if got, err := routes.ChatRoute(ctx, "pinned"); err != nil || got != nil {
		t.Fatal("clear failed", got, err)
	}
	if err := routes.SetChatRoute(ctx, "missing", &pin); err == nil {
		t.Fatal("orphan override accepted")
	}
}
func TestSessionModelLegacyDatabaseMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.(*sqliteStore).db.Exec(`DROP TABLE conversation_chat_routes`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.(chatroute.Store).ChatRoute(t.Context(), "old"); err != nil || got != nil {
		t.Fatal(got, err)
	}
}

func TestSessionModelClearIsIdempotentBeforeFirstTurn(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.(chatroute.Store).SetChatRoute(t.Context(), "new-session", nil); err != nil {
		t.Fatal(err)
	}
}
