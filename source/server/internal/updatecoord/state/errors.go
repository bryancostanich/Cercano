package state

import "errors"

// Refusal sentinels. Callers match with errors.Is. Every refusal carries a
// machine-readable reason; none of them embeds raw user reasons, tokens, or
// secrets. Corrupt data is never silently treated as empty and never
// triggers a destructive reset.
var (
	// ErrInvalidInstallID: the installation identifier is not a safe
	// opaque directory component.
	ErrInvalidInstallID = errors.New("state: invalid installation identifier")
	// ErrInvalidStateRoot: the supplied state root is not an absolute
	// path (or is otherwise unusable) for the target platform.
	ErrInvalidStateRoot = errors.New("state: invalid state root")
	// ErrUnsupportedPlatform: StatePath was asked about a platform this
	// build has no approved location for.
	ErrUnsupportedPlatform = errors.New("state: unsupported platform")
	// ErrUnsafePath: a path component in the state chain or the database
	// file itself is a symlink or not the expected kind (directory or
	// regular file). Existing directories are never chmodded instead.
	ErrUnsafePath = errors.New("state: unsafe state path")
	// ErrForeignDatabase: the existing database is not a Cercano updater
	// state database (wrong application_id, wrong user_version lineage,
	// unknown/extra tables, or a nonempty unknown database). No
	// migration, no reset.
	ErrForeignDatabase = errors.New("state: foreign database")
	// ErrFutureSchema: the database's schema version is NEWER than this
	// build supports. Refused without modification.
	ErrFutureSchema = errors.New("state: future schema")
	// ErrCorruptDatabase: the database or a stored row is malformed and
	// cannot be interpreted. Refused without modification; never treated
	// as empty.
	ErrCorruptDatabase = errors.New("state: corrupt database")
	// ErrInstallIDMismatch: the database's recorded installation identity
	// or a record's installation identity does not match the store's
	// installation.
	ErrInstallIDMismatch = errors.New("state: installation identity mismatch")
	// ErrStaleRevision: a compare-and-swap save lost the race — the
	// record's revision moved since it was read. The write was not
	// applied; a stale older operation record can never overwrite a
	// newer one.
	ErrStaleRevision = errors.New("state: stale revision")
	// ErrRecordNotFound: the requested record does not exist.
	ErrRecordNotFound = errors.New("state: record not found")
	// ErrInvalidRecord: the record failed validation before any write.
	ErrInvalidRecord = errors.New("state: invalid record")
)
