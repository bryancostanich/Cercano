package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"cercano/source/server/internal/updatecoord/operation"
	"cercano/source/server/internal/updatecoord/policy"
)

// legacyV1Database builds a fixture that is exactly the schema-1 database
// this package historically wrote: the legacy DDL, application_id,
// user_version 1, the meta rows, the install counter, and (optionally)
// one persisted operation record and one persisted delegation record. The
// fixture directory lives under the test's TempDir; nothing here touches a
// production location.
func legacyV1Database(t *testing.T, root, installID string) string {
	t.Helper()
	dir := filepath.Join(root, orgComponent(), "updater", installID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, stateFileName)
	if err := os.WriteFile(dbPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteURI(dbPath, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck // fixture teardown
	fail := func(e error) {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		t.Fatal(e)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		fail(err)
	}
	for _, q := range []string{
		schema1SQL,
		fmt.Sprintf("PRAGMA application_id = %d;", AppID),
		fmt.Sprintf("PRAGMA user_version = %d;", LegacySchemaVersion),
	} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			fail(err)
		}
	}
	meta := `INSERT INTO state_meta (key, value) VALUES (?, '1'), (?, ?)`
	if _, err := conn.ExecContext(ctx, meta, metaKeySchemaVersion, metaKeyInstallID, installID); err != nil {
		fail(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO install_state (install_id, next_op_id) VALUES (?, 2)`, installID); err != nil {
		fail(err)
	}
	// One committed legacy operation record (identifier 1, counter now 2).
	model := operation.NewStore()
	snap, err := model.Start(installID, "1.2.3")
	if err != nil {
		fail(err)
	}
	rec := snap.Record()
	rec.ID = 1
	payload, err := json.Marshal(rec)
	if err != nil {
		fail(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO operation_records (op_id, install_id, revision, record_json) VALUES (1, ?, 1, ?)`,
		installID, string(payload)); err != nil {
		fail(err)
	}
	// One committed legacy delegation record.
	pol := policy.DelegationRecord{
		SchemaVersion:        policy.DelegationSchemaVersion,
		InstallID:            installID,
		CanonicalRoot:        `C:\Users\me\AppData\Local\Cercano`,
		Owner:                "chocolatey",
		Platform:             "windows",
		Arch:                 "amd64",
		Scope:                "user",
		ReleaseChannel:       "stable",
		Source:               "tuf",
		FeedID:               "feed-stable",
		ConsentRecorded:      true,
		RegistrationRetained: true,
		ContractVersion:      "1",
	}
	polPayload, err := pol.Encode()
	if err != nil {
		fail(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO policy_records (install_id, revision, record_json) VALUES (?, 1, ?)`,
		installID, string(polPayload)); err != nil {
		fail(err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		fail(err)
	}
	return dbPath
}

// rawDB opens a read-write sqlite connection straight to a fixture path for
// preparing or inspecting database files; used only on parent TempDir
// fixtures inside this package's tests.
func rawDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteURI(dbPath, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func rawVersion(t *testing.T, dbPath string) int64 {
	t.Helper()
	db := rawDB(t, dbPath)
	var v int64
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func fileBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func noSidecars(t *testing.T, dbPath string) {
	t.Helper()
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(dbPath + suffix); !os.IsNotExist(err) {
			t.Fatalf("sidecar %s exists: %v", suffix, err)
		}
	}
}

func TestFreshDatabaseInitializesSchema2(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root, "test-install")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dbPath := filepath.Join(root, orgComponent(), "updater", "test-install", stateFileName)
	if v := rawVersion(t, dbPath); v != SchemaVersion {
		t.Fatalf("fresh user_version = %d", v)
	}
	db := rawDB(t, dbPath)
	var meta string
	if err := db.QueryRow(`SELECT value FROM state_meta WHERE key = ?`, metaKeySchemaVersion).Scan(&meta); err != nil || meta != "2" {
		t.Fatalf("fresh meta schema_version = %q %v", meta, err)
	}
	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='dismissal_records'`).Scan(&tables); err != nil || tables != 1 {
		t.Fatalf("dismissal_records missing: %d %v", tables, err)
	}
	ctx := context.Background()
	rev, err := s.SaveDismissalRecord(ctx, 0, policy.Dismissal{
		InstallID: "test-install", Channel: "stable", Source: "tuf", Version: "1.2.3",
	})
	if err != nil || rev != 1 {
		t.Fatalf("fresh dismissal save %d %v", rev, err)
	}
}

func TestLegacyV1UpgradesPreservingRecordsCounterAndPolicy(t *testing.T) {
	root := t.TempDir()
	dbPath := legacyV1Database(t, root, "test-install")

	s, err := Open(root, "test-install")
	if err != nil {
		t.Fatalf("upgrade open failed: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	if v := rawVersion(t, dbPath); v != SchemaVersion {
		t.Fatalf("user_version after upgrade = %d", v)
	}
	db := rawDB(t, dbPath)
	var meta string
	if err := db.QueryRow(`SELECT value FROM state_meta WHERE key = ?`, metaKeySchemaVersion).Scan(&meta); err != nil || meta != "2" {
		t.Fatalf("meta schema_version after upgrade = %q %v", meta, err)
	}
	var dismissed int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dismissal_records`).Scan(&dismissed); err != nil || dismissed != 0 {
		t.Fatalf("upgraded dismissal_records not empty: %d %v", dismissed, err)
	}

	// Every legacy operation record, the identifier counter, and the
	// delegation record survive the upgrade untouched.
	loaded, rev, err := s.LoadOperationRecord(ctx, 1)
	if err != nil || rev != 1 || loaded.ID != 1 || loaded.TargetVersion != "1.2.3" {
		t.Fatalf("legacy operation record lost: %+v %d %v", loaded, rev, err)
	}
	id, err := s.AllocateOperationID(ctx)
	if err != nil || id != 2 {
		t.Fatalf("counter after upgrade = %d %v", id, err)
	}
	pol, prev, err := s.LoadPolicyRecord(ctx)
	if err != nil || prev != 1 || pol.InstallID != "test-install" || pol.Owner != "chocolatey" {
		t.Fatalf("legacy delegation record lost: %+v %d %v", pol, prev, err)
	}

	// The upgraded installation immediately persists dismissals.
	key := policy.Dismissal{InstallID: "test-install", Channel: "stable", Source: "tuf", Version: "2.0.0"}
	if rev, err = s.SaveDismissalRecord(ctx, 0, key); err != nil || rev != 1 {
		t.Fatalf("post-upgrade dismissal save %d %v", rev, err)
	}
}

func TestMigrationFailureRollsBackSchema1Logically(t *testing.T) {
	root := t.TempDir()
	dbPath := legacyV1Database(t, root, "test-install")
	before := fileBytes(t, dbPath)

	migrationFault = func() error { return errors.New("injected migration failure") }
	defer func() { migrationFault = nil }()
	if _, err := Open(root, "test-install"); err == nil {
		t.Fatal("faulted migration unexpectedly succeeded")
	}

	// The database is logically still an untouched, complete schema-1
	// database: same version, same meta, same rows, no partial table.
	if v := rawVersion(t, dbPath); v != LegacySchemaVersion {
		t.Fatalf("user_version after failed migration = %d", v)
	}
	db := rawDB(t, dbPath)
	var meta string
	if err := db.QueryRow(`SELECT value FROM state_meta WHERE key = ?`, metaKeySchemaVersion).Scan(&meta); err != nil || meta != "1" {
		t.Fatalf("meta schema_version after failed migration = %q %v", meta, err)
	}
	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='dismissal_records'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("partial dismissal_records survived rollback: %d %v", tables, err)
	}
	var ops, next int64
	if err := db.QueryRow(`SELECT COUNT(*), (SELECT next_op_id FROM install_state) FROM operation_records`).Scan(&ops, &next); err != nil || ops != 1 || next != 2 {
		t.Fatalf("legacy data changed: ops=%d next=%d %v", ops, next, err)
	}
	_ = before

	// With the fault cleared the same database upgrades cleanly, proving
	// nothing about it was left half-migrated.
	migrationFault = nil
	s, err := Open(root, "test-install")
	if err != nil {
		t.Fatalf("retry open after rollback failed: %v", err)
	}
	defer s.Close()
	if v := rawVersion(t, dbPath); v != SchemaVersion {
		t.Fatalf("user_version after retry = %d", v)
	}
	if _, _, err = s.LoadOperationRecord(context.Background(), 1); err != nil {
		t.Fatalf("legacy record lost after retry: %v", err)
	}
}

func TestConcurrentLegacyOpensBothSucceedAtVersion2(t *testing.T) {
	root := t.TempDir()
	dbPath := legacyV1Database(t, root, "test-install")

	start := make(chan struct{})
	var wg sync.WaitGroup
	stores := make([]*Store, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			s, err := Open(root, "test-install")
			stores[i], errs[i] = s, err
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent open %d failed: %v", i, err)
		}
		defer stores[i].Close()
	}
	if v := rawVersion(t, dbPath); v != SchemaVersion {
		t.Fatalf("user_version after concurrent upgrade = %d", v)
	}
	db := rawDB(t, dbPath)
	var meta string
	if err := db.QueryRow(`SELECT value FROM state_meta WHERE key = ?`, metaKeySchemaVersion).Scan(&meta); err != nil || meta != "2" {
		t.Fatalf("meta schema_version after concurrent upgrade = %q %v", meta, err)
	}
	var dismissalTables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='dismissal_records'`).Scan(&dismissalTables); err != nil || dismissalTables != 1 {
		t.Fatalf("dismissal_records created %d times", dismissalTables)
	}
	// Both handles are usable against the upgraded database.
	ctx := context.Background()
	key := policy.Dismissal{InstallID: "test-install", Channel: "stable", Source: "tuf", Version: "2.0.0"}
	if _, err := stores[0].SaveDismissalRecord(ctx, 0, key); err != nil {
		t.Fatalf("handle0 save: %v", err)
	}
	if _, _, err := stores[1].LoadDismissalRecord(ctx, key); err != nil {
		t.Fatalf("handle1 load: %v", err)
	}
}

func TestFutureSchema3UnchangedWithoutSidecarsOrPermissionEdits(t *testing.T) {
	root := t.TempDir()
	dbPath := legacyV1Database(t, root, "test-install")
	// Advance the fixture to a future version the way a NEWER build would:
	// current tables, user_version 3.
	db := rawDB(t, dbPath)
	if _, err := db.Exec(schema2AddendumSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d;", SchemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	before := fileBytes(t, dbPath)
	beforeMode := modeOf(t, dbPath)

	_, err := Open(root, "test-install")
	if !errors.Is(err, ErrFutureSchema) {
		t.Fatalf("future schema open: %v", err)
	}
	if after := fileBytes(t, dbPath); string(after) != string(before) {
		t.Fatal("future database was modified")
	}
	if afterMode := modeOf(t, dbPath); afterMode != beforeMode {
		t.Fatalf("future database permission changed: %v -> %v", beforeMode, afterMode)
	}
	noSidecars(t, dbPath)
}

func TestLegacyAlteredSchemaObjectsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		sql  string
	}{
		{"extra table", `CREATE TABLE unexpected (x TEXT)`},
		{"trigger", `CREATE TRIGGER unexpected AFTER INSERT ON operation_records BEGIN SELECT 1; END`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dbPath := legacyV1Database(t, root, "test-install")
			db := rawDB(t, dbPath)
			if _, err := db.Exec(tc.sql); err != nil {
				t.Fatal(err)
			}
			before := fileBytes(t, dbPath)
			if _, err := Open(root, "test-install"); err == nil {
				t.Fatal("altered legacy schema was accepted")
			}
			if after := fileBytes(t, dbPath); string(after) != string(before) {
				t.Fatal("refused legacy database was modified")
			}
			if v := rawVersion(t, dbPath); v != LegacySchemaVersion {
				t.Fatalf("refused legacy database migrated to %d", v)
			}
			noSidecars(t, dbPath)
		})
	}
}

func TestLegacyMissingTableRefused(t *testing.T) {
	root := t.TempDir()
	dbPath := legacyV1Database(t, root, "test-install")
	db := rawDB(t, dbPath)
	if _, err := db.Exec(`DROP TABLE policy_records`); err != nil {
		t.Fatal(err)
	}
	before := fileBytes(t, dbPath)
	if _, err := Open(root, "test-install"); err == nil {
		t.Fatal("incomplete legacy schema was accepted for migration")
	}
	if after := fileBytes(t, dbPath); string(after) != string(before) {
		t.Fatal("refused incomplete legacy database was modified")
	}
	if v := rawVersion(t, dbPath); v != LegacySchemaVersion {
		t.Fatalf("refused incomplete legacy database migrated to %d", v)
	}
	noSidecars(t, dbPath)
}

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode()
}
