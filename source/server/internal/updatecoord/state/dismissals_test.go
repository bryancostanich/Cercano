package state

import (
	"context"
	"errors"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"cercano/source/server/internal/updatecoord/policy"
)

// dismissalKey is a valid dismissal bound to the test installation.
func dismissalKey(version string) policy.Dismissal {
	return policy.Dismissal{InstallID: "test-install", Channel: "stable", Source: "tuf", Version: version}
}

func TestDismissalPersistsAcrossReopenAndHandles(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root, "test-install")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := dismissalKey("1.2.3")
	if rev, err := s.SaveDismissalRecord(ctx, 0, key); err != nil || rev != 1 {
		t.Fatalf("save %d %v", rev, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// The dismissal survives a close/reopen cycle and is visible to a
	// second concurrently-open handle of the same installation.
	s, err = Open(root, "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	other, err := Open(root, "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	for _, store := range []*Store{s, other} {
		got, rev, err := store.LoadDismissalRecord(ctx, key)
		if err != nil || rev != 1 || got != key {
			t.Fatalf("load after reopen: %+v rev=%d err=%v", got, rev, err)
		}
	}
	list, err := s.ListDismissalRecords(ctx)
	if err != nil || len(list) != 1 || list[0] != key {
		t.Fatalf("list after reopen: %+v err=%v", list, err)
	}
}

func TestDismissalScopeIsExactChannelSourceVersionInstall(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root, "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	key := dismissalKey("1.2.3")
	if _, err := s.SaveDismissalRecord(ctx, 0, key); err != nil {
		t.Fatal(err)
	}
	// Every other announcement — different channel, source, version, or
	// installation — is NOT suppressed by the stored dismissal.
	for _, other := range []policy.Dismissal{
		{InstallID: "test-install", Channel: "beta", Source: "tuf", Version: "1.2.3"},
		{InstallID: "test-install", Channel: "stable", Source: "github", Version: "1.2.3"},
		{InstallID: "test-install", Channel: "stable", Source: "tuf", Version: "1.2.4"},
		{InstallID: "test-install", Channel: "stable", Source: "tuf", Version: "1.2.3.9"},
	} {
		if _, _, err := s.LoadDismissalRecord(ctx, other); !errors.Is(err, ErrRecordNotFound) {
			t.Fatalf("dismissal leaked to %+v: %v", other, err)
		}
	}
	// A different installation's store never sees this installation's
	// dismissal even against the identical announcement key.
	foreign, err := Open(root, "other-install")
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	if _, _, err := foreign.LoadDismissalRecord(ctx, key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("dismissal leaked across installations: %v", err)
	}
	if list, err := foreign.ListDismissalRecords(ctx); err != nil || len(list) != 0 {
		t.Fatalf("foreign list: %+v err=%v", list, err)
	}
	// A fresh installation has a clean, valid (empty) dismissal list.
	if list, err := s.ListDismissalRecords(ctx); err != nil || len(list) != 1 {
		t.Fatalf("own list: %+v err=%v", list, err)
	}
}

func TestDismissalCASInsertUpdateClear(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root, "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	key := dismissalKey("1.2.3")

	// Invalid expected revisions are refused before any write.
	if _, err := s.SaveDismissalRecord(ctx, -1, key); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("negative revision accepted: %v", err)
	}
	if _, err := s.SaveDismissalRecord(ctx, 0, key); err != nil {
		t.Fatal(err)
	}
	// Insert over an existing entry loses the CAS race.
	if _, err := s.SaveDismissalRecord(ctx, 0, key); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("duplicate insert: %v", err)
	}
	// Update with a stale revision is refused and applies nothing.
	if _, err := s.SaveDismissalRecord(ctx, 1, key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveDismissalRecord(ctx, 1, key); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale update accepted: %v", err)
	}
	if _, rev, err := s.LoadDismissalRecord(ctx, key); err != nil || rev != 2 {
		t.Fatalf("revision after refused save: %d %v", rev, err)
	}
	// Update on a nonexistent entry is stale too, not a silent insert.
	missing := dismissalKey("9.9.9")
	if _, err := s.SaveDismissalRecord(ctx, 2, missing); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("update of missing entry: %v", err)
	}

	// Clear is the same compare-and-save: stale refused, exact applied,
	// missing typed not-found, and a cleared key may be re-dismissed.
	if err := s.ClearDismissalRecord(ctx, key, 1); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale clear accepted: %v", err)
	}
	if err := s.ClearDismissalRecord(ctx, missing, 1); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("clear of missing entry: %v", err)
	}
	if err := s.ClearDismissalRecord(ctx, key, 2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.LoadDismissalRecord(ctx, key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("cleared dismissal still present: %v", err)
	}
	if list, err := s.ListDismissalRecords(ctx); err != nil || len(list) != 0 {
		t.Fatalf("list after clear: %+v err=%v", list, err)
	}
	if rev, err := s.SaveDismissalRecord(ctx, 0, key); err != nil || rev != 1 {
		t.Fatalf("re-dismiss after clear: %d %v", rev, err)
	}
}

func TestDismissalConcurrentCAS(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root, "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	key := dismissalKey("1.2.3")
	if _, err := s.SaveDismissalRecord(ctx, 0, key); err != nil {
		t.Fatal(err)
	}
	const n = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	saved := make(chan int64, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if rev, err := s.SaveDismissalRecord(ctx, 1, key); err == nil {
				saved <- rev
			} else if !errors.Is(err, ErrStaleRevision) {
				t.Errorf("concurrent save: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(saved)
	if len(saved) != 1 {
		t.Fatalf("exactly one CAS winner expected, got %d", len(saved))
	}
	if _, rev, err := s.LoadDismissalRecord(ctx, key); err != nil || rev != 2 {
		t.Fatalf("revision after race: %d %v", rev, err)
	}
}

func TestDismissalCrossInstallRefused(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root, "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	// Saving a record naming another installation is refused outright.
	foreign := policy.Dismissal{InstallID: "other-install", Channel: "stable", Source: "tuf", Version: "1.2.3"}
	if _, err := s.SaveDismissalRecord(ctx, 0, foreign); !errors.Is(err, ErrInstallIDMismatch) {
		t.Fatalf("foreign save accepted: %v", err)
	}
	// A row smuggled into this database whose key says this installation
	// but whose record names another fails closed — it is never treated
	// as absent, reset, or silently bound here.
	db := rawDB(t, dbFile(root, "test-install"))
	payload, err := foreign.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO dismissal_records (install_id, channel, source, version, revision, record_json)
		VALUES ('test-install', 'stable', 'tuf', '4.0.0', 1, ?)`, string(payload)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.LoadDismissalRecord(ctx, policy.Dismissal{InstallID: "test-install", Channel: "stable", Source: "tuf", Version: "4.0.0"}); !errors.Is(err, ErrInstallIDMismatch) {
		t.Fatalf("foreign record in row accepted: %v", err)
	}
	if _, err := s.ListDismissalRecords(ctx); !errors.Is(err, ErrInstallIDMismatch) {
		t.Fatalf("foreign record in list accepted: %v", err)
	}
}

func TestDismissalMalformedRowsFailClosed(t *testing.T) {
	key := dismissalKey("1.2.3")
	valid, err := key.Encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		version string
		payload string
	}{
		{"not json", "1.2.3", `not json at all`},
		{"unknown field", "1.2.3", `{"schema_version":1,"install_id":"test-install","channel":"stable","source":"tuf","version":"1.2.3","extra":true}`},
		{"future record schema", "1.2.3", `{"schema_version":2,"install_id":"test-install","channel":"stable","source":"tuf","version":"1.2.3"}`},
		{"trailing data", "1.2.3", string(valid) + ` {}`},
		{"row key mismatch", "9.9.9", string(valid)},
		{"empty field", "1.2.3", `{"schema_version":1,"install_id":"test-install","channel":"","source":"tuf","version":"1.2.3"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			s, err := Open(root, "test-install")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			db := rawDB(t, dbFile(root, "test-install"))
			if _, err := db.Exec(`INSERT INTO dismissal_records (install_id, channel, source, version, revision, record_json)
				VALUES ('test-install', 'stable', 'tuf', ?, 1, ?)`, tc.version, tc.payload); err != nil {
				t.Fatal(err)
			}
			lookup := policy.Dismissal{InstallID: "test-install", Channel: "stable", Source: "tuf", Version: tc.version}
			if _, _, err := s.LoadDismissalRecord(context.Background(), lookup); !errors.Is(err, ErrCorruptDatabase) {
				t.Fatalf("malformed row not refused closed: %v", err)
			}
			if _, err := s.ListDismissalRecords(context.Background()); !errors.Is(err, ErrCorruptDatabase) {
				t.Fatalf("malformed row survived list: %v", err)
			}
			// A non-positive revision is corrupt, not a clean entry.
			if _, err := db.Exec(`UPDATE dismissal_records SET revision = 0`); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.LoadDismissalRecord(context.Background(), lookup); !errors.Is(err, ErrCorruptDatabase) {
				t.Fatalf("zero revision not refused closed: %v", err)
			}
		})
	}
}

func TestDismissalSaveFaultRollsBack(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root, "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	key := dismissalKey("1.2.3")
	if _, err := s.SaveDismissalRecord(ctx, 0, key); err != nil {
		t.Fatal(err)
	}
	s.fault = func() error { return errors.New("injected write failure") }
	defer func() { s.fault = nil }()
	if _, err := s.SaveDismissalRecord(ctx, 1, key); err == nil {
		t.Fatal("faulted save succeeded")
	}
	// The earlier record is untouched: the whole write transaction rolled
	// back, so no revision was burned and no partial row exists.
	if got, rev, err := s.LoadDismissalRecord(ctx, key); err != nil || rev != 1 || got != key {
		t.Fatalf("record after faulted save: %+v rev=%d err=%v", got, rev, err)
	}
	if list, err := s.ListDismissalRecords(ctx); err != nil || len(list) != 1 {
		t.Fatalf("list after faulted save: %+v err=%v", list, err)
	}
}

func TestDismissalInvalidRecordRefusedBeforeWrite(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root, "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for _, bad := range []policy.Dismissal{
		{InstallID: "test-install", Channel: "", Source: "tuf", Version: "1.2.3"},
		{InstallID: "test-install", Channel: "stable", Source: "tuf", Version: ""},
	} {
		if _, err := s.SaveDismissalRecord(ctx, 0, bad); !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("invalid dismissal accepted: %+v %v", bad, err)
		}
	}
	db := rawDB(t, dbFile(root, "test-install"))
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dismissal_records`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid dismissal wrote a row: %d %v", count, err)
	}
}
