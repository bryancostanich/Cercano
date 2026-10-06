package state

import (
	"context"
	"errors"
	"testing"
)

func TestDismissalForeignKeyCannotReadOrClearLocalRecord(t *testing.T) {
	s, e := Open(t.TempDir(), "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	key := dismissalKey("1.2.3")
	rev, e := s.SaveDismissalRecord(ctx, 0, key)
	if e != nil {
		t.Fatal(e)
	}
	foreign := key
	foreign.InstallID = "other-install"
	if _, _, e = s.LoadDismissalRecord(ctx, foreign); !errors.Is(e, ErrInstallIDMismatch) {
		t.Errorf("foreign key load: %v", e)
	}
	if e = s.ClearDismissalRecord(ctx, foreign, rev); !errors.Is(e, ErrInstallIDMismatch) {
		t.Errorf("foreign key clear: %v", e)
	}
	if _, _, e = s.LoadDismissalRecord(ctx, key); e != nil {
		t.Fatalf("foreign key changed local record: %v", e)
	}
}
func TestDismissalRevisionSurvivesClearAndRecreate(t *testing.T) {
	root := t.TempDir()
	s, e := Open(root, "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	key := dismissalKey("1.2.3")
	old, e := s.SaveDismissalRecord(ctx, 0, key)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ClearDismissalRecord(ctx, key, old); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(root, "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	_, cleared, e := s.LoadDismissalRecord(ctx, key)
	if !errors.Is(e, ErrRecordNotFound) || cleared <= old {
		t.Fatalf("clear lost revision history: %d %v", cleared, e)
	}
	current, e := s.SaveDismissalRecord(ctx, cleared, key)
	if e != nil || current <= cleared {
		t.Fatalf("recreate %d %v", current, e)
	}
	if e = s.ClearDismissalRecord(ctx, key, old); !errors.Is(e, ErrStaleRevision) {
		t.Fatalf("old clear affected recreated row: %v", e)
	}
	if _, e = s.SaveDismissalRecord(ctx, 0, key); !errors.Is(e, ErrStaleRevision) {
		t.Fatalf("stale absent token accepted: %v", e)
	}
}
