package state

import (
	"context"
	"testing"
	"time"
)

func TestOpenRetriesTransientLegacyReadLock(t *testing.T) {
	root := t.TempDir()
	p := legacyV1Database(t, root, "test-install")
	holder := rawDB(t, p)
	conn, e := holder.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	if _, e = conn.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); e != nil {
		t.Fatal(e)
	}
	released := make(chan error, 1)
	go func() {
		time.Sleep(350 * time.Millisecond)
		_, err := conn.ExecContext(context.Background(), "ROLLBACK")
		released <- err
	}()
	defer func() {
		if err := <-released; err != nil {
			t.Error(err)
		}
	}()
	s, e := Open(root, "test-install")
	if e != nil {
		t.Fatalf("temporary read/open contention was not retried: %v", e)
	}
	defer s.Close()
	if id, e := s.AllocateOperationID(context.Background()); e != nil || id != 2 {
		t.Fatalf("legacy counter not preserved: %d %v", id, e)
	}
}
