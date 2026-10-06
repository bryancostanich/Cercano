package state

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	// The project's existing pure-Go SQLite driver. No new dependency.
	_ "modernc.org/sqlite"
)

// Tuning constants. All waits are bounded: the driver-level busy timeout
// bounds cross-process lock contention and every operation additionally
// honors its caller's context (with an internal cap so a missing deadline
// cannot hang a caller indefinitely).
const (
	busyTimeoutMS = 100
	opTimeout     = 10 * time.Second
	stateFileName = "state.db"
)

// Store is the SQLite-backed updater state store for ONE installation. It is
// a narrow, transactional primitive: monotonic operation IDs, revisioned
// records, and compare-and-swap saves/loads. It does not implement the
// operation lifecycle itself — see the package documentation for the
// adapter that is deliberately the next slice.
//
// A Store is safe for concurrent use from multiple goroutines (all calls
// serialize on the single pinned connection), and multiple Store handles in
// different processes may point at the same database: writers serialize
// through SQLite's file lock with the bounded busy timeout, and every save
// is a revision compare-and-swap, so a stale revision cannot overwrite a newer revision of the same record.
// Active-operation selection and event replay belong to the adapter, not this primitive.
type Store struct {
	db        *sql.DB
	installID string

	// fault is a white-box test hook invoked inside every write transaction
	// after BEGIN IMMEDIATE; a non-nil error forces a rollback. It is nil
	// in production and never set outside this package's tests.
	fault func() error
}

// Open opens (creating if necessary) the updater state database for
// installID beneath the explicit, host-absolute stateRoot. The
// root/Cercano/updater/<installID> chain is created if missing; on Unix each
// directory this package CREATES is private (0o700), while pre-existing
// directories are validated but NEVER chmodded — no arbitrary permission
// changes to directories this package does not own.
//
// Security posture and its limits:
//
//   - Every component of the chain this package manages and the database
//     file itself must be a real directory / regular file: a pre-existing
//     symlink or other non-regular entry anywhere in the managed chain is
//     refused (ErrUnsafePath). This is a preflight refusal, not a descriptor-anchored defense against
//     a concurrent same-user filesystem replacement.
//   - On Unix, 0o700 mode bits give per-user protection. On Windows, mode
//     bits carry no access-control meaning: this package does NOT claim any
//     protection from them. Enforcing a real Windows ACL on the state
//     directory is PENDING and must land before native Windows enrollment
//     writes real state.
//   - The state root itself is caller-trusted: only the Cercano/updater/
//     <installID> chain under it is managed here.
//
// An existing database is validated as the dedicated, recognized updater
// schema (application_id, user_version, exact table set for the database's
// own version, and meta-bound installID) BEFORE any write or journal pragma
// touches it. A legitimate legacy schema-1 database is upgraded in ONE
// immediate transaction to the current version (additive dismissal_records
// table plus the recorded version) with every existing operation record,
// identifier counter, and delegation record retained; the upgrade is
// revalidated under the same connection and is safe against a concurrent
// handle performing the same upgrade. Foreign, future, corrupt, or
// identity-mismatched databases are refused without modification; there is
// no reset and no destructive migration. A brand-new (absent) database is
// initialized as the recognized current schema version in one transaction.
//
// Open never reads the user's real environment: stateRoot and installID
// must come from the caller (tests use temporary directories).
func Open(stateRoot, installID string) (*Store, error) {
	dbPath, existing, err := prepareStateLocation(stateRoot, installID)
	if err != nil {
		return nil, err
	}

	// Read-only preflight prevents unknown databases from being opened writable.
	if existing {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		err := preflightValidate(ctx, dbPath, installID)
		cancel()
		if err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", sqliteURI(dbPath, "rw"))
	if err != nil {
		return nil, fmt.Errorf("state: open database: %w", err)
	}
	// Pin to a single physical connection (same reasoning as the
	// conversation store: one connection, one consistent view; every call
	// serializes here anyway) and bound lock contention.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	fail := func(err error) (*Store, error) {
		db.Close() //nolint:errcheck // already failing
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()

	// busy_timeout is a connection-local setting and does not touch the
	// database file; it runs before validation so contention with another
	// process does not misreport as corruption. No journal-mode pragma
	// runs until the existing database has passed validation, so a refused
	// database is never modified and no WAL sidecars are created.
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d;", busyTimeoutMS)); err != nil {
		return fail(fmt.Errorf("state: set busy timeout: %w", err))
	}

	if existing {
		// An existing database is either the recognized current version or
		// the exact legacy schema-1 version this package historically
		// wrote. Version 1 upgrades here — and ONLY here — in ONE
		// immediate transaction revalidated under this same connection,
		// which is safe against a concurrent handle performing the same
		// upgrade. Every other database stays refused without
		// modification; there is no reset and no destructive migration.
		conn, cerr := db.Conn(ctx)
		if cerr != nil {
			return fail(fmt.Errorf("state: connect: %w", cerr))
		}
		version, verr := readSchemaVersion(ctx, conn)
		if verr == nil && version == LegacySchemaVersion {
			verr = migrateSchema1To2(ctx, conn, installID)
		} else if verr == nil {
			// Validate on the SAME connection this handle already holds;
			// the pool is pinned to one connection.
			verr = validateExistingDB(ctx, conn, installID)
		}
		if verr != nil {
			_ = conn.Close()
			return fail(verr)
		}
		if cerr = conn.Close(); cerr != nil {
			return fail(fmt.Errorf("state: release connection: %w", cerr))
		}
	} else {
		if err := initNewDB(ctx, db, installID); err != nil {
			return fail(err)
		}
	}

	// Only after validation (or fresh initialization): WAL for durability
	// and crash safety.
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode = WAL; PRAGMA synchronous = FULL;"); err != nil {
		return fail(fmt.Errorf("state: set journal mode: %w", err))
	}

	return &Store{db: db, installID: installID}, nil
}

// Close closes the store's database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// InstallID returns the opaque installation identifier this store is bound
// to.
func (s *Store) InstallID() string {
	return s.installID
}

// prepareStateLocation validates the caller-supplied state root and the
// installation identifier, creates the managed
// root/<Cercano|cercano>/updater/<installID> chain if missing (creating
// each missing directory as 0o700 on Unix and never chmodding an existing
// one), refuses any symlink or non-directory/non-regular entry in the
// managed chain, and returns the database path plus whether an existing
// (nonzero) database file is present. An existing zero-byte file is refused as possibly truncated data.
func prepareStateLocation(stateRoot, installID string) (dbPath string, existing bool, err error) {
	if err := ValidateInstallID(installID); err != nil {
		return "", false, err
	}
	if !filepath.IsAbs(stateRoot) || filepath.Clean(stateRoot) != stateRoot {
		return "", false, fmt.Errorf("%w: %q is not an absolute path", ErrInvalidStateRoot, stateRoot)
	}
	info, err := os.Lstat(stateRoot)
	if err != nil {
		return "", false, fmt.Errorf("%w: cannot inspect state root: %v", ErrInvalidStateRoot, err)
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return "", false, fmt.Errorf("%w: state root %q is not a real directory", ErrInvalidStateRoot, stateRoot)
	}

	// The managed chain under the caller-trusted root. The organization
	// component casing follows the host platform's approved location
	// (Linux uses a lowercase "cercano"; Windows and macOS use
	// "Cercano"). Only these components are managed: directories above
	// the root are never created or modified.
	chain := []string{orgComponent(), "updater", installID}
	dir := stateRoot
	for _, comp := range chain {
		next := filepath.Join(dir, comp)
		info, err := os.Lstat(next)
		switch {
		case err == nil:
			if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
				return "", false, fmt.Errorf("%w: %q exists but is not a real directory", ErrUnsafePath, next)
			}
			// Pre-existing directory: validated, but deliberately NOT
			// chmodded — this package never changes permissions of
			// directories it did not create.
		case os.IsNotExist(err):
			if err := os.Mkdir(next, 0700); err != nil && !os.IsExist(err) {
				return "", false, fmt.Errorf("state: create directory: %w", err)
			}
			info, err = os.Lstat(next)
			if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
				return "", false, ErrUnsafePath
			}
		default:
			return "", false, fmt.Errorf("state: inspect %q: %w", next, err)
		}
		if runtime.GOOS != "windows" && info != nil && info.Mode().Perm()&0022 != 0 {
			return "", false, ErrUnsafePath
		}
		dir = next
	}

	dbPath = filepath.Join(dir, stateFileName)
	// SQLite also opens these paths; refuse links/nonregular sidecars before
	// even the read-only schema preflight reaches the driver.
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		entry, e := os.Lstat(dbPath + suffix)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return "", false, fmt.Errorf("%w: unreadable sidecar", ErrUnsafePath)
		}
		if !entry.Mode().IsRegular() || entry.Mode()&fs.ModeSymlink != 0 || (runtime.GOOS != "windows" && entry.Mode().Perm()&0077 != 0) {
			return "", false, ErrUnsafePath
		}
	}
	info, err = os.Lstat(dbPath)
	switch {
	case err == nil:
		if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", false, fmt.Errorf("%w: %q is not a regular file", ErrUnsafePath, dbPath)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			return "", false, ErrUnsafePath
		}
		if info.Size() == 0 {
			return "", false, fmt.Errorf("%w: existing database is empty", ErrCorruptDatabase)
		}
		return dbPath, true, nil
	case os.IsNotExist(err):
		f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return "", false, fmt.Errorf("state: create private database: %w", err)
		}
		if err = f.Close(); err != nil {
			return "", false, err
		}
		return dbPath, false, nil
	default:
		return "", false, fmt.Errorf("state: inspect database: %w", err)
	}
}

// orgComponent returns the organization directory component for the HOST
// platform's approved location.
func orgComponent() string {
	if runtime.GOOS == platformLinux {
		return "cercano"
	}
	return "Cercano"
}

// inWriteTx runs fn inside a BEGIN IMMEDIATE transaction on the store's
// single pinned connection. IMMEDIATE takes the write lock up front, so
// concurrent writers in other processes serialize on the bounded busy
// timeout instead of failing mid-transaction, and the whole mutation is
// atomic: fn's statements and the revision/counter update either all commit
// or all roll back. On any error the transaction is rolled back, leaving
// the database exactly as it was.
func (s *Store) inWriteTx(ctx context.Context, fn func(ctx context.Context, conn *sql.Conn) error) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	for {
		_, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE")
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var code interface{ Code() int }
		if !errors.As(err, &code) || code.Code()&0xff != 5 {
			return fmt.Errorf("state: begin write transaction: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	err = fn(ctx, conn)
	if err == nil {
		if _, cerr := conn.ExecContext(ctx, "COMMIT"); cerr != nil {
			err = fmt.Errorf("state: commit: %w", cerr)
		}
	}
	if err != nil {
		// Roll back with a fresh bounded context; the original ctx may
		// already be the reason we are failing.
		rctx, rcancel := context.WithTimeout(context.Background(), opTimeout)
		defer rcancel()
		if _, rerr := conn.ExecContext(rctx, "ROLLBACK"); rerr != nil {
			// Never return a possibly open transaction to the connection pool.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			return fmt.Errorf("state: rollback after failure (%v) also failed: %w", err, rerr)
		}
		return err
	}
	return nil
}

// AllocateOperationID returns the next operation identifier for this
// installation. Identifiers are one strictly monotonically increasing
// sequence per installation, persisted transactionally, so they are unique
// across processes and STABLE ACROSS RESTARTS — a restart never reissues an
// identifier. The counter update and the read happen inside one BEGIN
// IMMEDIATE transaction, so two racing processes can never observe the same
// identifier.
func (s *Store) AllocateOperationID(ctx context.Context) (int64, error) {
	var id int64
	err := s.inWriteTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		next, err := s.allocateOperationIDConn(ctx, conn)
		if err != nil {
			return err
		}
		id = next
		return nil
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// allocateOperationIDConn is the conn-scoped counter primitive. It MUST be
// called inside a caller-owned BEGIN IMMEDIATE transaction: composite
// operations (the operation adapter) share ONE transaction across counter
// allocation and record writes instead of the exported per-operation
// helpers, which each open their own transaction. Failing composite
// transactions roll the counter back with everything else, so an aborted
// identifier is never burned as a gap.
func (s *Store) allocateOperationIDConn(ctx context.Context, conn *sql.Conn) (int64, error) {
	if s.fault != nil {
		if err := s.fault(); err != nil {
			return 0, err
		}
	}
	var next int64
	row := conn.QueryRowContext(ctx,
		`UPDATE install_state SET next_op_id = next_op_id + 1 WHERE install_id = ? AND typeof(next_op_id)='integer' AND next_op_id >= 1 AND next_op_id < 9223372036854775807 RETURNING next_op_id`,
		s.installID)
	if err := row.Scan(&next); err != nil {
		return 0, fmt.Errorf("%w: installation state row missing: %v", ErrCorruptDatabase, err)
	}
	return next - 1, nil
}

func sqliteURI(p, mode string) string {
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	q := url.Values{}
	q.Set("mode", mode)
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS))
	q.Add("_pragma", "synchronous(FULL)")
	u.RawQuery = q.Encode()
	return u.String()
}
