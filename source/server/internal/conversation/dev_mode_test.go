package conversation

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestDevModeLegacyDatabaseMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	original, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := original.EnsureConversation(ctx, "legacy", "/tmp/project", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := original.(*sqliteStore).db.ExecContext(ctx, `ALTER TABLE conversations DROP COLUMN dev_work_dir`); err != nil {
		t.Fatal(err)
	}
	original.Close()
	migrated, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	info, err := migrated.Get(ctx, "legacy")
	if err != nil || info.DevWorkDir != "" || info.ProjectDir != "/tmp/project" {
		t.Fatalf("legacy metadata changed: %+v %v", info, err)
	}
	if err := migrated.SetDevWorkDir(ctx, "legacy", "/tmp/dev-repo"); err != nil {
		t.Fatal(err)
	}
	list, err := migrated.List(ctx, "", 0)
	if err != nil || len(list) != 1 || list[0].DevWorkDir != "/tmp/dev-repo" {
		t.Fatalf("list metadata lost: %+v %v", list, err)
	}
	if err := migrated.SetDevWorkDir(ctx, "missing", "/tmp/dev-repo"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("missing conversation silently accepted", err)
	}
	if err := migrated.SetDevWorkDir(ctx, "legacy", "relative/path"); err == nil {
		t.Fatal("relative dev directory persisted")
	}
}
func TestDevModeFollowsConversationRollover(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.EnsureConversation(ctx, "before", "/tmp/original", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDevWorkDir(ctx, "before", "/tmp/dev-repo"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRolledOver(ctx, "after", "/tmp/original", "", "before", Turn{Role: "user", Content: "handoff"}); err != nil {
		t.Fatal(err)
	}
	info, err := s.Get(ctx, "after")
	if err != nil || info.DevWorkDir != "/tmp/dev-repo" {
		t.Fatalf("rollover lost explicit dev mode: %+v %v", info, err)
	}
}
