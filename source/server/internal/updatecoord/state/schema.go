package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AppID is the SQLite application_id marking a database as Cercano updater
// state ("CRCN"). It is the first, cheapest dedication check: a database
// with any other application_id is foreign and refused before anything else
// is read or written.
const AppID = 0x4352434E // 'C','R','C','N'

// SchemaVersion is the recognized schema version. Version 3 adds ONLY the
// activation_journals table — the first ADDITIVE step of the approved
// SQLite activation-journal slice, mirroring the operation-record pattern
// (keyed by operation identifier, installation-bound, revisioned, strict
// JSON) so the next slice can attach a journal API without another schema
// step; no journal write/read API or live data exists yet. Older
// databases — version 1 and version 2, and ONLY those — are upgraded in
// one immediate transaction inside Open after full revalidation: a
// validated schema-1 database chains sequentially through every
// intermediate version to the current one, and a validated schema-2
// database takes the remaining single step. The exact legacy schema-1 and
// schema-2 definitions are preserved for that validation and are never
// treated as an unknown/empty database or overwritten by guessing. A
// database with any other user_version (including a future one) is
// refused without modification, and no destructive migration, table
// drop, or reset exists.
const SchemaVersion = 3

// LegacySchemaVersion is the OLDEST schema version a database may carry to
// be upgraded. Every version from here up to (but not including)
// SchemaVersion is recognized, validated against its own exact table set,
// and upgraded additively.
const LegacySchemaVersion = 1

// meta keys stored in state_meta and validated on every open.
const (
	metaKeySchemaVersion = "schema_version"
	metaKeyInstallID     = "install_id"
)

// schema1Tables is the exact table set of schema version 1, kept verbatim so
// a legacy database is validated against precisely the schema this package
// used to write — never as an unknown or empty database.
var schema1Tables = map[string]bool{
	"state_meta":        true,
	"install_state":     true,
	"operation_records": true,
	"policy_records":    true,
}

// schema2Tables is the exact table set of schema version 2: the legacy
// version-1 tables plus the dedicated dismissal_records table. An existing
// version-2 database must contain exactly these tables (ignoring sqlite's
// internal sqlite_% tables): missing tables mean a partial or corrupt write,
// extra tables mean a foreign or future database. Both are refused.
var schema2Tables = map[string]bool{
	"state_meta":        true,
	"install_state":     true,
	"operation_records": true,
	"policy_records":    true,
	"dismissal_records": true,
}

// schema3Tables is the exact table set of schema version 3: the version-2
// tables plus the activation_journals table. The same exactness rules
// apply: a database claiming version 3 must contain exactly these tables
// or it is refused as partial, corrupt, foreign, or future.
var schema3Tables = map[string]bool{
	"state_meta":         true,
	"install_state":      true,
	"operation_records":  true,
	"policy_records":     true,
	"dismissal_records":  true,
	"activation_journals": true,
}

// schema1SQL is the version-1 schema, byte-for-byte the definition this
// package historically wrote. It is preserved EXACTLY: legacy databases are
// validated against this text, and the version-2 upgrade creates only the
// additive dismissal_records table on top of it — no version-1 object is
// ever redefined, recreated, or dropped.
const schema1SQL = `
CREATE TABLE state_meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) WITHOUT ROWID;

CREATE TABLE install_state (
  install_id      TEXT PRIMARY KEY,
  next_op_id      INTEGER NOT NULL
) WITHOUT ROWID;

CREATE TABLE operation_records (
  op_id       INTEGER PRIMARY KEY,
  install_id  TEXT NOT NULL,
  revision    INTEGER NOT NULL,
  record_json TEXT NOT NULL
);

CREATE TABLE policy_records (
  install_id   TEXT PRIMARY KEY,
  revision     INTEGER NOT NULL,
  record_json  TEXT NOT NULL
) WITHOUT ROWID;
`

// schema2AddendumSQL is the ONLY schema change from version 1 to version 2:
// a dedicated dismissal_records table holding one durable per-version
// dismissal per installation. Each row is addressed by the installation and
// the dismissed announcement's exact channel/source/version key and carries
// a per-entry revision (compare-and-save) plus the strict
// policy.DismissalRecord JSON.
const schema2AddendumSQL = `
CREATE TABLE dismissal_records (
  install_id   TEXT NOT NULL,
  channel      TEXT NOT NULL,
  source       TEXT NOT NULL,
  version      TEXT NOT NULL,
  revision     INTEGER NOT NULL,
  active       INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
  record_json  TEXT NOT NULL,
  PRIMARY KEY (install_id, channel, source, version)
) WITHOUT ROWID;
`

// schema3AddendumSQL is the ONLY schema change from version 2 to version 3:
// the activation_journals table, the first ADDITIVE step of the approved
// SQLite activation-journal slice. It deliberately mirrors the
// operation_records pattern — keyed by operation identifier, bound to one
// installation, per-row revision for future compare-and-save journal writes,
// and a strict canonical JSON payload column — so journals ride the same
// storage discipline as operation records. The journal payload format is
// NOT defined by this slice and no journal write/read API exists yet: the
// table is created empty and migration merely adds it.
const schema3AddendumSQL = `
CREATE TABLE activation_journals (
  op_id        INTEGER PRIMARY KEY,
  install_id   TEXT NOT NULL,
  revision     INTEGER NOT NULL,
  journal_json TEXT NOT NULL
);
`

// schemaAddendumSQL returns the ONLY DDL the single migration step from the
// given recognized version adds. Steps are additive: they append new
// objects and never redefine or drop a version-1 or version-2 object.
func schemaAddendumSQL(from int64) string {
	switch from {
	case 1:
		return schema2AddendumSQL
	case 2:
		return schema3AddendumSQL
	default:
		return ""
	}
}

// schemaSQLFor returns the full DDL text of a recognized schema version.
func schemaSQLFor(version int64) string {
	switch version {
	case 1:
		return schema1SQL
	case 2:
		return schema1SQL + schema2AddendumSQL
	case 3:
		return schema1SQL + schema2AddendumSQL + schema3AddendumSQL
	default:
		return ""
	}
}

// tablesFor returns the exact table set of a recognized schema version.
func tablesFor(version int64) map[string]bool {
	switch version {
	case 1:
		return schema1Tables
	case 2:
		return schema2Tables
	case 3:
		return schema3Tables
	default:
		return nil
	}
}

// initNewDB initializes a brand-new exclusively created database as a
// recognized current-schema updater state database for installID, in ONE
// transaction: schema, application_id, user_version, the meta rows, and the
// install counter row either all commit or none do. It is never run
// against a database that already has content.
func initNewDB(ctx context.Context, db *sql.DB, installID string) error {
	if err := exec(ctx, db, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("state: begin init transaction: %w", err)
	}
	fail := func(err error) error {
		exec(context.Background(), db, "ROLLBACK") //nolint:errcheck // best effort; conn closes on failure
		return err
	}
	stmts := []string{
		schemaSQLFor(SchemaVersion),
		fmt.Sprintf("PRAGMA application_id = %d;", AppID),
		fmt.Sprintf("PRAGMA user_version = %d;", SchemaVersion),
	}
	for _, q := range stmts {
		if err := exec(ctx, db, q); err != nil {
			return fail(fmt.Errorf("state: init: %w", err))
		}
	}
	ins := `INSERT INTO state_meta (key, value) VALUES (?, ?), (?, ?)`
	if _, err := db.ExecContext(ctx, ins,
		metaKeySchemaVersion, fmt.Sprint(SchemaVersion),
		metaKeyInstallID, installID); err != nil {
		return fail(fmt.Errorf("state: init meta: %w", err))
	}
	counter := `INSERT INTO install_state (install_id, next_op_id) VALUES (?, 1)`
	if _, err := db.ExecContext(ctx, counter, installID); err != nil {
		return fail(fmt.Errorf("state: init counter: %w", err))
	}
	if err := exec(ctx, db, "COMMIT"); err != nil {
		return fail(fmt.Errorf("state: commit init: %w", err))
	}
	return nil
}

// dbtx is the query/statement surface shared by *sql.DB and *sql.Conn, so
// the same strict validation runs on the read-only preflight connection, the
// writable connection, and — critically — inside the migration transaction
// on the very connection that then performs the upgrade.
type dbtx interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// exec runs a raw statement (transaction control or pragma).
func exec(ctx context.Context, db dbtx, query string) error {
	if _, err := db.ExecContext(ctx, query); err != nil {
		return err
	}
	return nil
}

// validateExistingDB checks that an existing database is a dedicated,
// recognized updater state database bound to installID. The WHOLE
// recognized schema for the database's own version is validated: each
// recognized version (1, 2, or 3) must match the exact definition for
// that version — same version with unknown/extra/missing/altered objects
// is refused. The exact legacy schema-1 and schema-2 definitions are
// preserved verbatim, so an older database is never treated as an unknown
// or empty one. It is strictly read-only: no write and no journal pragma
// runs before it succeeds. Every anomaly is a typed refusal — foreign,
// future, corrupt, or identity mismatch — and none of them modifies the
// file or resets anything.
func validateExistingDB(ctx context.Context, db dbtx, installID string) error {
	var appid int64
	if err := db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&appid); err != nil {
		return fmt.Errorf("%w: cannot read application_id: %w", ErrCorruptDatabase, err)
	}
	if appid != AppID {
		return fmt.Errorf("%w: application_id %#x", ErrForeignDatabase, appid)
	}

	version, err := readSchemaVersion(ctx, db)
	if err != nil {
		return err
	}
	tables := tablesFor(version)
	if tables == nil {
		if version > SchemaVersion {
			return fmt.Errorf("%w: user_version %d > supported %d", ErrFutureSchema, version, SchemaVersion)
		}
		return fmt.Errorf("%w: user_version %d", ErrForeignDatabase, version)
	}

	expected := map[string]string{}
	normalize := func(s string) string {
		return strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimSpace(s), ";")), " ")
	}
	for _, q := range strings.Split(schemaSQLFor(version), ";") {
		fields := strings.Fields(q)
		if len(fields) > 2 {
			expected[fields[2]] = normalize(q)
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT type,name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return fmt.Errorf("%w: unreadable schema", ErrCorruptDatabase)
	}
	seen := 0
	for rows.Next() {
		var kind, name string
		var definition sql.NullString
		if err = rows.Scan(&kind, &name, &definition); err != nil {
			rows.Close()
			return ErrCorruptDatabase
		}
		if kind != "table" || !tables[name] || !definition.Valid || normalize(definition.String) != expected[name] {
			rows.Close()
			return fmt.Errorf("%w: unrecognized schema object", ErrForeignDatabase)
		}
		seen++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ErrCorruptDatabase
	}
	if seen != len(tables) {
		return fmt.Errorf("%w: incomplete schema", ErrCorruptDatabase)
	}

	meta := map[string]string{}
	mrows, err := db.QueryContext(ctx, `SELECT key, value FROM state_meta`)
	if err != nil {
		return fmt.Errorf("%w: cannot read state_meta: %v", ErrCorruptDatabase, err)
	}
	defer mrows.Close()
	for mrows.Next() {
		var k, v string
		if err := mrows.Scan(&k, &v); err != nil {
			return fmt.Errorf("%w: malformed state_meta row: %v", ErrCorruptDatabase, err)
		}
		meta[k] = v
	}
	if err := mrows.Err(); err != nil {
		return fmt.Errorf("%w: reading state_meta: %v", ErrCorruptDatabase, err)
	}
	if len(meta) != 2 {
		return fmt.Errorf("%w: state_meta is empty", ErrCorruptDatabase)
	}
	// The persisted meta version must agree with the file's user_version —
	// including for a legacy version-1 database, which must still say "1".
	if got := meta[metaKeySchemaVersion]; got != fmt.Sprint(version) {
		return fmt.Errorf("%w: meta schema_version %q", ErrCorruptDatabase, got)
	}
	if got := meta[metaKeyInstallID]; got != installID {
		return fmt.Errorf("%w: database is bound to installation %q", ErrInstallIDMismatch, got)
	}
	var count, next int64
	var kind string
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM install_state`).Scan(&count); err != nil || count != 1 {
		return ErrCorruptDatabase
	}
	if err := db.QueryRowContext(ctx, `SELECT next_op_id,typeof(next_op_id) FROM install_state WHERE install_id=?`, installID).Scan(&next, &kind); err != nil || kind != "integer" || next < 1 {
		return ErrCorruptDatabase
	}
	return nil
}

// preflightValidate opens the database read-only and validates the whole
// recognized schema for the database's own version inside ONE consistent
// read transaction, so a concurrent version upgrade (another handle
// migrating a legacy database) can never be observed half-applied. It performs
// no schema/data write or journal-mode change. SQLite may access existing WAL
// coordination state; this is not a promise of byte-immutable shared-memory locks.
func preflightValidate(ctx context.Context, dbPath, installID string) error {
	ro, err := sql.Open("sqlite", sqliteURI(dbPath, "ro"))
	if err != nil {
		return err
	}
	defer ro.Close()
	ro.SetMaxOpenConns(1)
	conn, err := ro.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck // read-only preflight path
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return fmt.Errorf("%w: cannot begin read snapshot: %w", ErrCorruptDatabase, err)
	}
	verr := validateExistingDB(ctx, conn, installID)
	// End the snapshot. ROLLBACK of a read-only transaction never writes
	// the database file.
	rctx, rcancel := context.WithTimeout(context.Background(), opTimeout)
	defer rcancel()
	_, _ = conn.ExecContext(rctx, "ROLLBACK") //nolint:errcheck // best-effort snapshot end
	return verr
}

// readSchemaVersion reads the database's user_version and rejects
// unreadable values.
func readSchemaVersion(ctx context.Context, db dbtx) (int64, error) {
	var version int64
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("%w: cannot read user_version: %w", ErrCorruptDatabase, err)
	}
	return version, nil
}

// migrationFault is a white-box test hook invoked inside the migration
// transaction immediately BEFORE each step's state_meta schema-version
// update commits; a non-nil error forces a rollback of the whole
// transaction — the step's DDL and user_version bump roll back together
// with the metadata update, proving no partial table or partial version
// can ever survive a failed migration. It is nil in production and never
// set outside this package's tests.
var migrationFault func() error

// migrateLegacySchema upgrades a legitimate OLDER database to the current
// schema version. It MUST run on the writable connection that will perform
// the upgrade. The whole operation — revalidation of the exact schema for
// the database's own version under the SAME connection, then one
// ADDITIVE step per intermediate version — happens in ONE BEGIN IMMEDIATE
// transaction: a validated version-1 database chains SEQUENTIALLY through
// version 2 to version 3, and a validated version-2 database takes the
// remaining single step. Every step only appends new objects (no version-1
// or version-2 object is ever redefined, recreated, or dropped) and moves
// the user_version and the state_meta schema_version marker together, so
// every existing operation record, identifier counter, delegation record,
// and dismissal record is retained unchanged and a failure at ANY point
// leaves the database logically still the complete version it started as.
//
// Concurrent safety: the write lock is taken up front with the same bounded
// busy retry as every other write, and the version is RE-READ under that
// lock — a second handle that opens while another is upgrading simply
// waits and then observes the finished upgrade, revalidates the
// now-current schema, and commits a no-op. A version newer than this
// build supports (a concurrent process from a newer build) rolls back and
// is refused as a future schema without a single write; anything else
// unexpected is refused — there is no guessing, no reset, and no
// destructive migration.
func migrateLegacySchema(ctx context.Context, conn *sql.Conn, installID string) error {
	if err := beginImmediate(ctx, conn); err != nil {
		return fmt.Errorf("state: begin migration transaction: %w", err)
	}
	rollback := func() {
		rctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		_, _ = conn.ExecContext(rctx, "ROLLBACK") //nolint:errcheck // best effort; error path only
	}
	commit := func() error {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			rollback()
			return fmt.Errorf("state: commit migration: %w", err)
		}
		return nil
	}

	// Re-read the version under the write lock: a concurrent opener may
	// have completed (or, from a newer build, advanced) the upgrade while
	// this handle was waiting for the lock.
	version, err := readSchemaVersion(ctx, conn)
	if err != nil {
		rollback()
		return err
	}
	switch {
	case version == SchemaVersion:
		// A version number alone is not proof of a legitimate concurrent
		// upgrade. Revalidate layout and identity under this write lock too.
		if err := validateExistingDB(ctx, conn, installID); err != nil {
			rollback()
			return err
		}
		return commit()
	case version >= LegacySchemaVersion && version < SchemaVersion:
		// Revalidate the exact schema — table set, DDL, application
		// identity, installation identity, and the strict meta rows —
		// under the SAME connection holding the write lock, so the schema
		// this transaction upgrades cannot change underneath it.
		if err := validateExistingDB(ctx, conn, installID); err != nil {
			rollback()
			return err
		}
	default:
		rollback()
		if version > SchemaVersion {
			return fmt.Errorf("%w: user_version %d > supported %d", ErrFutureSchema, version, SchemaVersion)
		}
		return fmt.Errorf("%w: user_version %d", ErrForeignDatabase, version)
	}

	// Sequential additive steps through every intermediate version; the
	// chain commits atomically as a whole or not at all.
	for from := version; from < SchemaVersion; from++ {
		to := from + 1
		if err := exec(ctx, conn, schemaAddendumSQL(from)); err != nil {
			rollback()
			return fmt.Errorf("state: apply schema %d addendum: %w", to, err)
		}
		if err := exec(ctx, conn, fmt.Sprintf("PRAGMA user_version = %d;", to)); err != nil {
			rollback()
			return fmt.Errorf("state: bump user_version to %d: %w", to, err)
		}
		// Test-injection point for the atomic-rollback proof; nil in
		// production. The fault lands immediately BEFORE this step's
		// metadata-version update commits, so a rollback must also erase
		// the step's DDL and user_version bump.
		if migrationFault != nil {
			if ferr := migrationFault(); ferr != nil {
				rollback()
				return ferr
			}
		}
		res, err := conn.ExecContext(ctx,
			`UPDATE state_meta SET value = ? WHERE key = ? AND value = ?`,
			fmt.Sprint(to), metaKeySchemaVersion, fmt.Sprint(from))
		if err != nil {
			rollback()
			return fmt.Errorf("state: update schema_version meta to %d: %w", to, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			rollback()
			return fmt.Errorf("state: update schema_version meta to %d: %w", to, err)
		}
		if n != 1 {
			rollback()
			return fmt.Errorf("%w: schema_version meta row did not move from %d", ErrCorruptDatabase, from)
		}
	}
	return commit()
}

// beginImmediate takes the write lock up front with the bounded busy retry
// shared by every write path: concurrent writers in other processes or on
// other handles serialize on the busy timeout instead of failing
// mid-transaction, and a missing deadline still cannot hang a caller.
func beginImmediate(ctx context.Context, conn *sql.Conn) error {
	for {
		_, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE")
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var code interface{ Code() int }
		if !errors.As(err, &code) || code.Code()&0xff != 5 {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
