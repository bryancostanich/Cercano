package state

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// AppID is the SQLite application_id marking a database as Cercano updater
// state ("CRCN"). It is the first, cheapest dedication check: a database
// with any other application_id is foreign and refused before anything else
// is read or written.
const AppID = 0x4352434E // 'C','R','C','N'

// SchemaVersion is the recognized schema version. Version 1 is the first
// schema this package writes. There is deliberately NO automatic upgrade:
// a database with any other user_version (including a future one) is
// refused without modification, and no migration — destructive or
// otherwise — exists in this slice.
const SchemaVersion = 1

// meta keys stored in state_meta and validated on every open.
const (
	metaKeySchemaVersion = "schema_version"
	metaKeyInstallID     = "install_id"
)

// knownTables is the exact table set of schema version 1. An existing
// database must contain exactly these tables (ignoring sqlite's internal
// sqlite_% tables): missing tables mean a partial or corrupt write, extra
// tables mean a foreign or future database. Both are refused.
var knownTables = map[string]bool{
	"state_meta":        true,
	"install_state":     true,
	"operation_records": true,
	"policy_records":    true,
}

// schemaSQL is the version-1 schema. It is created only for a NEW empty
// database, inside one transaction together with the application_id,
// user_version, and meta rows — so a fresh database is either fully
// initialized or not created at all.
const schemaSQL = `
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

// initNewDB initializes a brand-new (absent or zero-byte) database as a
// recognized version-1 updater state database for installID, in ONE
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
		schemaSQL,
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

// validateExistingDB checks that an existing database is a dedicated,
// recognized version-1 updater state database bound to installID. It is
// strictly read-only: no write and no journal pragma runs before it
// succeeds. Every anomaly is a typed refusal — foreign, future, corrupt, or
// identity mismatch — and none of them modifies the file or resets
// anything.
func validateExistingDB(ctx context.Context, db *sql.DB, installID string) error {
	var appid int64
	if err := db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&appid); err != nil {
		return fmt.Errorf("%w: cannot read application_id: %v", ErrCorruptDatabase, err)
	}
	if appid != AppID {
		return fmt.Errorf("%w: application_id %#x", ErrForeignDatabase, appid)
	}

	var uv int64
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&uv); err != nil {
		return fmt.Errorf("%w: cannot read user_version: %v", ErrCorruptDatabase, err)
	}
	if uv != SchemaVersion {
		if uv > SchemaVersion {
			return fmt.Errorf("%w: user_version %d > supported %d", ErrFutureSchema, uv, SchemaVersion)
		}
		return fmt.Errorf("%w: user_version %d", ErrForeignDatabase, uv)
	}

	expected := map[string]string{}
	normalize := func(s string) string {
		return strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimSpace(s), ";")), " ")
	}
	for _, q := range strings.Split(schemaSQL, ";") {
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
		if kind != "table" || !knownTables[name] || !definition.Valid || normalize(definition.String) != expected[name] {
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
	if seen != len(knownTables) {
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
	if got := meta[metaKeySchemaVersion]; got != fmt.Sprint(SchemaVersion) {
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

// exec runs a raw statement (transaction control or pragma).
func exec(ctx context.Context, db *sql.DB, query string) error {
	if _, err := db.ExecContext(ctx, query); err != nil {
		return err
	}
	return nil
}
