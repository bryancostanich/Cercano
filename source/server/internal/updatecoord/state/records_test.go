package state

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"cercano/source/server/internal/updatecoord/operation"
	"cercano/source/server/internal/updatecoord/policy"
)

func newRecord(t *testing.T, s *Store) operation.Record {
	t.Helper()
	id, e := s.AllocateOperationID(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	model := operation.NewStore()
	snap, e := model.Start(s.installID, "1.2.3")
	if e != nil {
		t.Fatal(e)
	}
	rec := snap.Record()
	rec.ID = id
	return rec
}
func TestRecordPersistenceAndCAS(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, e := Open(root, "test-install")
	if e != nil {
		t.Fatal(e)
	}
	rec := newRecord(t, s)
	rev, e := s.SaveOperationRecord(ctx, 0, rec)
	if e != nil || rev != 1 {
		t.Fatalf("save %d %v", rev, e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(root, "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	loaded, rev, e := s.LoadOperationRecord(ctx, rec.ID)
	// JSON intentionally drops Go's process-local monotonic clock component.
	if !loaded.CreatedAt.Equal(rec.CreatedAt) || !loaded.UpdatedAt.Equal(rec.UpdatedAt) {
		t.Fatal("persisted timestamp changed")
	}
	rec.CreatedAt, rec.UpdatedAt = loaded.CreatedAt, loaded.UpdatedAt
	if e != nil || loaded != rec || rev != 1 {
		t.Fatalf("reopen mismatch: %+v %d %v", loaded, rev, e)
	}
	other, e := Open(root, "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	if _, e = other.SaveOperationRecord(ctx, rev, rec); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveOperationRecord(ctx, rev, rec); !errors.Is(e, ErrStaleRevision) {
		t.Fatalf("stale save %v", e)
	}
	rec.InstallationID = "other-install"
	if _, e = s.SaveOperationRecord(ctx, 2, rec); !errors.Is(e, ErrInstallIDMismatch) {
		t.Fatalf("cross-install save %v", e)
	}
}
func TestWriteRollbackAndCounterExhaustion(t *testing.T) {
	s, e := Open(t.TempDir(), "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	injected := errors.New("fixture abort")
	e = s.inWriteTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, `UPDATE install_state SET next_op_id=100`); err != nil {
			return err
		}
		return injected
	})
	if !errors.Is(e, injected) {
		t.Fatalf("rollback did not return original error: %v", e)
	}
	id, e := s.AllocateOperationID(ctx)
	if e != nil || id != 1 {
		t.Fatalf("uncommitted counter leaked %d %v", id, e)
	}
	if _, e = s.db.Exec(`UPDATE install_state SET next_op_id=9223372036854775807`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AllocateOperationID(ctx); !errors.Is(e, ErrCorruptDatabase) {
		t.Fatalf("counter overflow allowed: %v", e)
	}
}
func TestPolicyRoundTripBindingAndCorruption(t *testing.T) {
	s, e := Open(t.TempDir(), "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	rec := policy.DelegationRecord{SchemaVersion: policy.DelegationSchemaVersion, InstallID: "test-install", CanonicalRoot: `C:\Users\me\AppData\Local\Cercano`, Owner: "chocolatey", Platform: "windows", Arch: "amd64", Scope: "user", ReleaseChannel: "stable", Source: "tuf", FeedID: "feed-stable", ConsentRecorded: true, RegistrationRetained: true, ContractVersion: "1"}
	rev, e := s.SavePolicyRecord(ctx, 0, rec)
	if e != nil || rev != 1 {
		t.Fatalf("save %d %v", rev, e)
	}
	got, rev, e := s.LoadPolicyRecord(ctx)
	if e != nil || got != rec || rev != 1 {
		t.Fatalf("load %+v %d %v", got, rev, e)
	}
	rec.InstallID = "other-install"
	if _, e = s.SavePolicyRecord(ctx, rev, rec); !errors.Is(e, ErrInstallIDMismatch) {
		t.Fatalf("binding check %v", e)
	}
	if _, e = s.db.Exec(`UPDATE policy_records SET record_json='{"schema_version":999}'`); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.LoadPolicyRecord(ctx); !errors.Is(e, ErrCorruptDatabase) {
		t.Fatalf("corruption fallback %v", e)
	}
}
func TestMalformedOperationIsNotEmpty(t *testing.T) {
	s, e := Open(t.TempDir(), "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	rec := newRecord(t, s)
	if _, e = s.SaveOperationRecord(ctx, 0, rec); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`UPDATE operation_records SET record_json='null'`); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.LoadOperationRecord(ctx, rec.ID); !errors.Is(e, ErrCorruptDatabase) {
		t.Fatalf("malformed row %v", e)
	}
}
