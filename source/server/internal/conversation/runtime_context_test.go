package conversation

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeContextUsageMigratesExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversations.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	oldSchema := strings.Replace(schemaSQL, "    provider TEXT NOT NULL DEFAULT '',\n", "", 1)
	oldSchema = strings.Replace(oldSchema, "    runtime_instance_id TEXT NOT NULL DEFAULT '',\n", "", 1)
	if _, err := db.Exec(oldSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO conversations(id,started_at,last_turn_at) VALUES('old',1,1); INSERT INTO conversation_context_usage(conversation_id,model,context_window,window_known) VALUES('old','local',8192,1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		store, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		usage, ok, err := store.GetContextUsage(t.Context(), "old")
		if err != nil || !ok || usage.ContextWindow != 8192 {
			t.Fatalf("lost old usage: %+v %v", usage, err)
		}
		if i == 0 && (usage.Provider != "" || usage.RuntimeInstanceID != "") {
			t.Fatal("migration invented runtime identity")
		}
		if i == 1 && (usage.Provider != "llama_server" || usage.RuntimeInstanceID != "process-1") {
			t.Fatal("runtime identity did not survive reopen")
		}
		usage.Provider = "llama_server"
		usage.RuntimeInstanceID = "process-1"
		if err := store.SaveContextUsage(t.Context(), usage); err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
