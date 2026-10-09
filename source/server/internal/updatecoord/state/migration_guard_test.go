package state

import (
	"context"
	"errors"
	"testing"
)

func TestConcurrentUpgradeNoopRevalidatesCurrentIdentity(t *testing.T) {
	s, e := Open(t.TempDir(), "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	if _, e = s.db.Exec(`UPDATE state_meta SET value='other-install' WHERE key='install_id'`); e != nil {
		t.Fatal(e)
	}
	conn, e := s.db.Conn(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	// The no-op branch of the migration must not skip identity validation.
	if e = migrateLegacySchema(ctx, conn, "test-install"); !errors.Is(e, ErrInstallIDMismatch) {
		t.Fatalf("already-upgraded branch bypassed identity validation: %v", e)
	}
}
