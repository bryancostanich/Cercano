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

// legacyV2Database builds a fixture that is exactly the schema-2 database
// this package historically wrote: the schema-1 DDL plus the additive
// dismissal_records addendum, application_id, user_version 2, the meta
// rows, the install counter, and one each of a persisted operation,
// delegation, and dismissal record. Like legacyV1Database it lives under
// the test's TempDir and never touches a production location.
func legacyV2Database(t *testing.T, root, installID string) string {
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
		schema2AddendumSQL,
		fmt.Sprintf("PRAGMA application_id = %d;", AppID),
		"PRAGMA user_version = 2;",
	} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			fail(err)
		}
	}
	meta := `INSERT INTO state_meta (key, value) VALUES (?, '2'), (?, ?)`
	if _, err := conn.ExecContext(ctx, meta, metaKeySchemaVersion, metaKeyInstallID, installID); err != nil {
		fail(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO install_state (install_id, next_op_id) VALUES (?, 2)`, installID); err != nil {
		fail(err)
	}
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
	dism := policy.Dismissal{InstallID: installID, Channel: "stable", Source: "tuf", Version: "1.2.3"}
	dismPayload, err := dism.Encode()
	if err != nil {
		fail(err)
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO dismissal_records (install_id, channel, source, version, revision, active, record_json) VALUES (?,?,?,?,1,1,?)`,
		installID, "stable", "tuf", "1.2.3", string(dismPayload)); err != nil {
		fail(err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		fail(err)
	}
	return dbPath
}

// TestSchema2UpgradesToSchema3PreservingEverything proves the single
// additive v2→v3 step: only activation_journals is created (empty), and
// every schema-2 record — operation, counter, delegation, and dismissal —
// keeps its exact semantics through the store's own APIs.
func TestSchema2UpgradesToSchema3PreservingEverything(t *testing.T) {
	root := t.TempDir()
	dbPath := legacyV2Database(t, root, "test-install")

	s, err := Open(root, "test-install")
	if err != nil {
		t.Fatalf("upgrade open failed: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	if v := rawVersion(t, dbPath); v != SchemaVersion {
		t.Fatalf("user_version after v2 upgrade = %d", v)
	}
	db := rawDB(t, dbPath)
	var meta string
	if err := db.QueryRow(`SELECT value FROM state_meta WHERE key = ?`, metaKeySchemaVersion).Scan(&meta); err != nil || meta != "3" {
		t.Fatalf("meta schema_version after v2 upgrade = %q %v", meta, err)
	}
	var journals int
	if err := db.QueryRow(`SELECT COUNT(*) FROM activation_journals`).Scan(&journals); err != nil || journals != 0 {
		t.Fatalf("upgraded activation_journals not empty: %d %v", journals, err)
	}

	loaded, rev, err := s.LoadOperationRecord(ctx, 1)
	if err != nil || rev != 1 || loaded.ID != 1 || loaded.TargetVersion != "1.2.3" {
		t.Fatalf("v2 operation record lost: %+v %d %v", loaded, rev, err)
	}
	id, err := s.AllocateOperationID(ctx)
	if err != nil || id != 2 {
		t.Fatalf("counter after v2 upgrade = %d %v", id, err)
	}
	pol, prev, err := s.LoadPolicyRecord(ctx)
	if err != nil || prev != 1 || pol.InstallID != "test-install" || pol.Owner != "chocolatey" {
		t.Fatalf("v2 delegation record lost: %+v %d %v", pol, prev, err)
	}
	key := policy.Dismissal{InstallID: "test-install", Channel: "stable", Source: "tuf", Version: "1.2.3"}
	d, drev, err := s.LoadDismissalRecord(ctx, key)
	if err != nil || drev != 1 || d.InstallID != "test-install" || d.Version != "1.2.3" {
		t.Fatalf("v2 dismissal record lost: %+v %d %v", d, drev, err)
	}
	list, err := s.ListDismissalRecords(ctx)
	if err != nil || len(list) != 1 || list[0].Version != "1.2.3" {
		t.Fatalf("v2 dismissal list lost: %+v %v", list, err)
	}
}

// TestSchema2To3FaultBeforeMetaCommitRollsBackWholeStep injects a failure
// after the activation_journals DDL and the user_version bump but
// immediately BEFORE the state_meta schema-version update commits. The
// step must roll back as a unit: no partial table, no partial version, and
// every v2 record untouched.
func TestSchema2To3FaultBeforeMetaCommitRollsBackWholeStep(t *testing.T) {
	root := t.TempDir()
	dbPath := legacyV2Database(t, root, "test-install")

	migrationFault = func() error { return errors.New("injected migration failure") }
	defer func() { migrationFault = nil }()
	if _, err := Open(root, "test-install"); err == nil {
		t.Fatal("faulted migration unexpectedly succeeded")
	}

	// Logically still a complete schema-2 database.
	if v := rawVersion(t, dbPath); v != 2 {
		t.Fatalf("user_version after failed v2 migration = %d", v)
	}
	db := rawDB(t, dbPath)
	var meta string
	if err := db.QueryRow(`SELECT value FROM state_meta WHERE key = ?`, metaKeySchemaVersion).Scan(&meta); err != nil || meta != "2" {
		t.Fatalf("meta schema_version after failed v2 migration = %q %v", meta, err)
	}
	var journals int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='activation_journals'`).Scan(&journals); err != nil || journals != 0 {
		t.Fatalf("partial activation_journals survived rollback: %d %v", journals, err)
	}
	var ops, next, dismissals int64
	if err := db.QueryRow(`SELECT COUNT(*), (SELECT next_op_id FROM install_state), (SELECT COUNT(*) FROM dismissal_records) FROM operation_records`).Scan(&ops, &next, &dismissals); err != nil || ops != 1 || next != 2 || dismissals != 1 {
		t.Fatalf("v2 data changed: ops=%d next=%d dismissals=%d %v", ops, next, dismissals, err)
	}

	// Cleared fault: the same database upgrades cleanly; nothing was left
	// half-migrated.
	migrationFault = nil
	s, err := Open(root, "test-install")
	if err != nil {
		t.Fatalf("retry open after rollback failed: %v", err)
	}
	defer s.Close()
	if v := rawVersion(t, dbPath); v != SchemaVersion {
		t.Fatalf("user_version after retry = %d", v)
	}
	key := policy.Dismissal{InstallID: "test-install", Channel: "stable", Source: "tuf", Version: "1.2.3"}
	if _, drev, err := s.LoadDismissalRecord(context.Background(), key); err != nil || drev != 1 {
		t.Fatalf("dismissal lost after retry: %d %v", drev, err)
	}
}

// TestV1ChainFaultInSecondStepRollsBackEntireChain proves the v1→v3 chain
// is ONE atomic transaction: a fault injected into the second (v2→v3) step
// rolls back the first step's dismissal_records DDL, user_version bump,
// and metadata update too — the database is left the complete schema-1
// database it started as, with no intermediate version ever observable.
func TestV1ChainFaultInSecondStepRollsBackEntireChain(t *testing.T) {
	root := t.TempDir()
	dbPath := legacyV1Database(t, root, "test-install")

	steps := 0
	migrationFault = func() error {
		steps++
		if steps == 1 {
			return nil // first (v1→v2) step proceeds
		}
		return errors.New("injected second-step failure")
	}
	defer func() { migrationFault = nil }()
	if _, err := Open(root, "test-install"); err == nil {
		t.Fatal("faulted chain migration unexpectedly succeeded")
	}

	if v := rawVersion(t, dbPath); v != LegacySchemaVersion {
		t.Fatalf("user_version after failed chain = %d", v)
	}
	db := rawDB(t, dbPath)
	var meta string
	if err := db.QueryRow(`SELECT value FROM state_meta WHERE key = ?`, metaKeySchemaVersion).Scan(&meta); err != nil || meta != "1" {
		t.Fatalf("meta schema_version after failed chain = %q %v", meta, err)
	}
	for _, table := range []string{"dismissal_records", "activation_journals"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("partial %s survived chain rollback: %d %v", table, n, err)
		}
	}
	var ops, next int64
	if err := db.QueryRow(`SELECT COUNT(*), (SELECT next_op_id FROM install_state) FROM operation_records`).Scan(&ops, &next); err != nil || ops != 1 || next != 2 {
		t.Fatalf("legacy data changed: ops=%d next=%d %v", ops, next, err)
	}

	migrationFault = nil
	s, err := Open(root, "test-install")
	if err != nil {
		t.Fatalf("retry open after chain rollback failed: %v", err)
	}
	defer s.Close()
	if v := rawVersion(t, dbPath); v != SchemaVersion {
		t.Fatalf("user_version after retry = %d", v)
	}
	if _, _, err = s.LoadOperationRecord(context.Background(), 1); err != nil {
		t.Fatalf("legacy record lost after retry: %v", err)
	}
}

// TestConcurrentSchema2OpensBothSucceedAtCurrentSchema runs two concurrent
// Open calls against a populated schema-2 database: one performs the
// migration, the other re-reads the version under the write lock and
// commits a no-op. Exactly one activation_journals table exists and both
// handles remain fully usable.
func TestConcurrentSchema2OpensBothSucceedAtCurrentSchema(t *testing.T) {
	root := t.TempDir()
	dbPath := legacyV2Database(t, root, "test-install")

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
		t.Fatalf("user_version after concurrent v2 upgrade = %d", v)
	}
	db := rawDB(t, dbPath)
	var meta string
	if err := db.QueryRow(`SELECT value FROM state_meta WHERE key = ?`, metaKeySchemaVersion).Scan(&meta); err != nil || meta != "3" {
		t.Fatalf("meta schema_version after concurrent v2 upgrade = %q %v", meta, err)
	}
	for _, table := range []string{"dismissal_records", "activation_journals"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s created %d times", table, n)
		}
	}
	ctx := context.Background()
	id, err := stores[0].AllocateOperationID(ctx)
	if err != nil || id != 2 {
		t.Fatalf("handle0 allocate: %d %v", id, err)
	}
	key := policy.Dismissal{InstallID: "test-install", Channel: "stable", Source: "tuf", Version: "1.2.3"}
	if _, _, err := stores[1].LoadDismissalRecord(ctx, key); err != nil {
		t.Fatalf("handle1 load: %v", err)
	}
}
