package state

// Durable activation journals.
//
// The user approved extending the existing SQLite updater store for
// authoritative activation intent and recovery facts; this file is that
// journal's TYPED RECORD and STRICT STORAGE API only. It deliberately does
// NOT implement the recovery executor: there is no filesystem selection
// writer, no activation, no restart, no health check, no cache cleanup, no
// PID authorization, no arbitrary command execution, and no self-update
// path here. Those belong to later slices that must reconcile the separate
// launcher-readable selection file against this journal instead of
// claiming cross-filesystem atomicity.
//
// Scope discipline: the operation record remains the authority for
// operation lifecycle. A journal row is only ever WRITTEN for an
// ALLOCATED, EXISTING operation that is the installation's CURRENT
// operation (highest persisted ID) and whose target version matches —
// inside the SAME transaction as the write — so no orphan, stale, or
// superseded writer can attach a journal to an operation it does not own,
// and the journal never duplicates or contradicts the operation record's
// target or identity.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"
)

// ActivationJournalSchemaVersion is the schema version ActivationJournal
// currently models. Stored payloads with any other schema version are
// refused, never coerced.
const ActivationJournalSchemaVersion = 1

// maxActivationJournalJSONBytes bounds the persisted journal payload (same
// bound as operation records): a malformed or hostile row cannot park
// unbounded data in the state database, and the bound is enforced on write
// before the payload can ever be stored.
const maxActivationJournalJSONBytes = 32 * 1024

// maxActivationVersionBytes bounds every version-like string in a journal.
const maxActivationVersionBytes = 256

// JournalCheckpoint is the mutable progress marker of one activation
// journal.
//
// A checkpoint records durable INTENT that the coordinator reached a
// decision point and committed that fact. It is NOT proof the effect
// happened on disk: the database commit and any filesystem selection
// change are two separate operations, and this package performs neither
// the latter nor any reconciliation. A launcher or executor reading the
// journal must treat every checkpoint as "the coordinator committed this
// intention", never as "the filesystem is known to be in this state".
//
// Checkpoint semantics beyond the fact set are deliberately MINIMAL and
// documented rather than invented: a journal is created ONLY as a
// prepared-intent record (nothing has happened yet), and every later save
// must move the checkpoint STRICTLY forward in journalCheckpointOrder.
// This slice enforces no recovery state machine, defines no legal
// transition table beyond that monotonicity, and performs no automatic
// action; the recovery executor slice defines legality and reconciliation.
type JournalCheckpoint string

const (
	// JournalPrepared is the minimal activation intent: the coordinator
	// has recorded WHAT it intends to do (immutable facts below) before
	// changing anything. Every journal is created at this checkpoint and
	// only at this checkpoint.
	JournalPrepared JournalCheckpoint = "prepared"
	// JournalSwitchIntent records that the coordinator is about to change
	// the selection. It precedes the switch, not proves it.
	JournalSwitchIntent JournalCheckpoint = "switch-intent"
	// JournalSelected records that the selection switch was committed to.
	// Not proof the selection file on disk agrees; reconciliation belongs
	// to the executor slice.
	JournalSelected JournalCheckpoint = "selected"
	// JournalHealthVerified records that health verification succeeded.
	JournalHealthVerified JournalCheckpoint = "health-verified"
	// JournalCleanupPending records that cleanup could not finish (for
	// example locked files) and remains explicitly pending.
	JournalCleanupPending JournalCheckpoint = "cleanup-pending"
	// JournalComplete records the activation completed successfully.
	JournalComplete JournalCheckpoint = "complete"
	// JournalRollbackIntent records that the coordinator intends to
	// restore the prior complete selection.
	JournalRollbackIntent JournalCheckpoint = "rollback-intent"
	// JournalRestored records that the restore was committed to. As with
	// every checkpoint, durable intent, not proof of disk state.
	JournalRestored JournalCheckpoint = "restored"
)

// journalCheckpointOrder is the single progression rule this slice enforces:
// strictly increasing. It is a monotonicity ordering only — it is not a
// transition- legality table, and nothing in this package acts on it.
var journalCheckpointOrder = map[JournalCheckpoint]int{
	JournalPrepared:       1,
	JournalSwitchIntent:   2,
	JournalSelected:       3,
	JournalHealthVerified: 4,
	JournalCleanupPending: 5,
	JournalComplete:       6,
	JournalRollbackIntent: 7,
	JournalRestored:       8,
}

// ActivationJournal is the typed, strict-JSON activation journal record
// for exactly ONE operation of ONE installation. One journal per
// operation; the operation's own record remains the lifecycle authority.
//
// Everything except Checkpoint is IMMUTABLE after the journal is created:
// SaveActivationJournal checks the incoming record's immutable facts
// against the persisted row in the SAME transaction and refuses any
// change to identity, target, staged identifier, verified artifact, or the
// prior-selection block. There is no way to reset or rewrite them.
type ActivationJournal struct {
	// SchemaVersion must equal ActivationJournalSchemaVersion.
	SchemaVersion int `json:"schema_version"`
	// InstallID is the installation the journal is bound to; must match
	// the store's installation exactly.
	InstallID string `json:"install_id"`
	// OpID is the existing, allocated operation this journal belongs to;
	// at every write it must be the installation's CURRENT operation.
	OpID int64 `json:"op_id"`
	// TargetVersion is the version being activated. It must equal the
	// owning operation record's target version — checked in the same
	// transaction as every write.
	TargetVersion string `json:"target_version"`
	// StagedVersionDir identifies the staged version directory as a
	// RELATIVE identifier only: never an absolute path, never a Windows
	// drive path, never a backslash path, never a `..` or `.` component.
	// It is an identifier for interpretation by the trusted executor, not
	// a path this package ever opens.
	StagedVersionDir string `json:"staged_version_dir"`
	// VerifiedArtifactSHA256 is the SHA-256 (64 lowercase hex) of the
	// verified artifact the staged directory was staged from — recorded
	// as captured at verification time, never re-derived.
	VerifiedArtifactSHA256 string `json:"verified_artifact_sha256"`
	// PriorSelectedVersion is the previously selected complete version.
	// An EXPLICIT "no prior selection" (a first activation) is
	// represented by the exact combination of empty version, generation
	// 0, and empty digest — every other combination with an empty
	// version is refused, so "none" is explicit, never guessed or
	// defaulted.
	PriorSelectedVersion string `json:"prior_selected_version"`
	// PriorSelectionGeneration is the prior selection's generation
	// counter as explicitly captured when the intent was recorded. It is
	// never reconstructed by this package; with no prior selection it
	// must be 0.
	PriorSelectionGeneration int64 `json:"prior_selection_generation"`
	// PriorSelectionDigest is the SHA-256 (64 lowercase hex) digest of
	// the prior selection as explicitly captured when the intent was
	// recorded, never re-derived; with no prior selection it must be
	// empty.
	PriorSelectionDigest string `json:"prior_selection_digest"`
	// Checkpoint is the journal's mutable progress marker; see
	// JournalCheckpoint for its deliberately minimal semantics.
	Checkpoint JournalCheckpoint `json:"checkpoint"`
}

// activationImmutableFacts is the comparable projection of every
// immutable journal fact. Checkpoint is deliberately excluded: only it
// may change between revisions.
type activationImmutableFacts struct {
	SchemaVersion            int
	InstallID                string
	OpID                     int64
	TargetVersion            string
	StagedVersionDir         string
	VerifiedArtifactSHA256   string
	PriorSelectedVersion     string
	PriorSelectionGeneration int64
	PriorSelectionDigest     string
}

func (j ActivationJournal) facts() activationImmutableFacts {
	return activationImmutableFacts{
		SchemaVersion:            j.SchemaVersion,
		InstallID:                j.InstallID,
		OpID:                     j.OpID,
		TargetVersion:            j.TargetVersion,
		StagedVersionDir:         j.StagedVersionDir,
		VerifiedArtifactSHA256:   j.VerifiedArtifactSHA256,
		PriorSelectedVersion:     j.PriorSelectedVersion,
		PriorSelectionGeneration: j.PriorSelectionGeneration,
		PriorSelectionDigest:     j.PriorSelectionDigest,
	}
}

// SaveActivationJournal validates j against the journal schema, this
// store's installation, and the installation's CURRENT operation record —
// then persists it under a compare-and-swap on the journal's revision.
//
// Revision rules mirror the operation-record primitive: expectedRevision
// 0 creates the journal (which must not already exist, and which must be a
// MINIMAL PREPARED-INTENT record: Checkpoint == JournalPrepared, nothing
// has happened yet); any other value must match the persisted revision
// exactly or the save fails with ErrStaleRevision and NOTHING is written.
// The insert/update, the current-operation check, and the immutable-facts
// check run in ONE BEGIN IMMEDIATE transaction, so a failed or interrupted
// save leaves the previously persisted journal fully intact.
//
// Authority and anti-orphan rules, all inside that same transaction:
//
//   - j.InstallID must equal this store's installation (ErrInstallIDMismatch).
//   - j.OpID must name an EXISTING, allocated operation — and specifically
//     the installation's CURRENT operation (highest persisted ID). A
//     nonexistent operation, an unallocated identifier, or a journal for a
//     superseded operation once a newer operation exists is refused with
//     ErrRecordNotFound or ErrNotCurrentOperation before any write.
//   - j.TargetVersion must equal the owning operation record's target
//     version; the journal cannot contradict the operation's own facts.
//   - On update, every immutable fact must match the persisted row
//     exactly; changing identity, target, staged identifier, verified
//     artifact, or the prior-selection block is refused (ErrInvalidRecord).
//   - The checkpoint must move STRICTLY forward in
//     journalCheckpointOrder; a downgrade — including rewriting or
//     re-saving a completed journal's checkpoint — is refused with
//     ErrInvalidRecord and nothing is written.
//
// This API performs no filesystem change, no process action, and no
// selection write: the checkpoint it persists is durable intent, not
// proof any effect happened on disk.
func (s *Store) SaveActivationJournal(ctx context.Context, expectedRevision int64, j ActivationJournal) (int64, error) {
	if expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return 0, fmt.Errorf("%w: invalid expected revision", ErrInvalidRecord)
	}
	if err := validateActivationJournal(j, s.installID); err != nil {
		return 0, err
	}
	payload, err := json.Marshal(j)
	if err != nil {
		return 0, fmt.Errorf("%w: marshal: %v", ErrInvalidRecord, err)
	}
	if len(payload) > maxActivationJournalJSONBytes {
		return 0, fmt.Errorf("%w: journal payload exceeds %d bytes", ErrInvalidRecord, maxActivationJournalJSONBytes)
	}
	var newRevision int64
	err = s.inWriteTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		rev, err := s.saveActivationJournalConn(ctx, conn, expectedRevision, j, payload)
		if err != nil {
			return err
		}
		newRevision = rev
		return nil
	})
	if err != nil {
		return 0, err
	}
	return newRevision, nil
}

// saveActivationJournalConn is the conn-scoped journal-write primitive. It
// MUST be called inside a caller-owned BEGIN IMMEDIATE transaction; the
// current-operation authority check and the immutable-facts/CAS checks
// share that transaction with the write itself.
func (s *Store) saveActivationJournalConn(ctx context.Context, conn *sql.Conn, expectedRevision int64, j ActivationJournal, payload []byte) (int64, error) {
	if s.fault != nil {
		if ferr := s.fault(); ferr != nil {
			return 0, ferr
		}
	}
	// Authority: the journal belongs to an EXISTING, allocated operation
	// that is the installation's CURRENT operation. The operation record
	// is loaded through the same strict, binding-checked path the adapter
	// uses; a journal can never name an operation that was never
	// allocated or persisted.
	rec, _, found, err := s.loadCurrentOperationConn(ctx, conn)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, fmt.Errorf("%w: no operation exists to own an activation journal", ErrRecordNotFound)
	}
	if j.OpID != rec.ID {
		return 0, fmt.Errorf("%w: journal names operation %d, current operation is %d", ErrNotCurrentOperation, j.OpID, rec.ID)
	}
	if j.TargetVersion != rec.TargetVersion {
		return 0, fmt.Errorf("%w: journal target %q does not match operation %d target %q", ErrInvalidRecord, j.TargetVersion, rec.ID, rec.TargetVersion)
	}

	if expectedRevision == 0 {
		// Creation is only ever a minimal prepared-intent record.
		if j.Checkpoint != JournalPrepared {
			return 0, fmt.Errorf("%w: a journal is created only at the prepared checkpoint", ErrInvalidRecord)
		}
		var count int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM activation_journals WHERE op_id=?`, j.OpID).Scan(&count); err != nil {
			return 0, err
		}
		if count != 0 {
			// One journal per operation, forever.
			return 0, ErrStaleRevision
		}
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO activation_journals (op_id, install_id, revision, journal_json) VALUES (?, ?, 1, ?)`,
			j.OpID, s.installID, string(payload)); err != nil {
			return 0, fmt.Errorf("state: insert activation journal: %w", err)
		}
		return 1, nil
	}

	// Update: strict-decode the persisted row, check immutable facts and
	// the revision, all in the same transaction as the write.
	var rowInstall string
	var current int64
	var previous string
	err = conn.QueryRowContext(ctx,
		`SELECT install_id, revision, journal_json FROM activation_journals WHERE op_id=?`, j.OpID).Scan(&rowInstall, &current, &previous)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrStaleRevision
	}
	if err != nil {
		return 0, fmt.Errorf("state: load activation journal: %w", err)
	}
	if rowInstall != s.installID {
		return 0, fmt.Errorf("%w: journal row names installation %q, store is %q", ErrInstallIDMismatch, rowInstall, s.installID)
	}
	if current < 1 {
		return 0, fmt.Errorf("%w: activation journal %d at revision %d", ErrCorruptDatabase, j.OpID, current)
	}
	stored, err := decodeActivationJournal([]byte(previous))
	if err != nil {
		return 0, fmt.Errorf("%w: activation journal %d: %v", ErrCorruptDatabase, j.OpID, err)
	}
	if stored.InstallID != s.installID || stored.OpID != j.OpID {
		return 0, fmt.Errorf("%w: activation journal %d is bound to a different installation", ErrInstallIDMismatch, j.OpID)
	}
	// Immutable facts may never change after creation.
	if stored.facts() != j.facts() {
		return 0, fmt.Errorf("%w: immutable activation facts cannot change", ErrInvalidRecord)
	}
	if current != expectedRevision {
		return 0, ErrStaleRevision
	}
	// Monotonic progression only: no downgrade, no rewrite, no reset of a
	// terminal checkpoint.
	if journalCheckpointOrder[j.Checkpoint] <= journalCheckpointOrder[stored.Checkpoint] {
		return 0, fmt.Errorf("%w: checkpoint %q cannot follow %q", ErrInvalidRecord, j.Checkpoint, stored.Checkpoint)
	}
	res, err := conn.ExecContext(ctx,
		`UPDATE activation_journals SET revision = revision + 1, journal_json = ? WHERE op_id = ? AND install_id = ? AND revision = ?`,
		string(payload), j.OpID, s.installID, expectedRevision)
	if err != nil {
		return 0, fmt.Errorf("state: update activation journal: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("state: update activation journal: %w", err)
	}
	if n == 0 {
		return 0, fmt.Errorf("%w: activation journal %d is not at revision %d", ErrStaleRevision, j.OpID, expectedRevision)
	}
	return expectedRevision + 1, nil
}

// LoadActivationJournal loads the persisted activation journal of the
// given operation together with its current revision for the next
// compare-and-save. A missing journal is ErrRecordNotFound. A row that
// fails strict decoding or validation — malformed JSON, duplicate keys,
// unknown fields, unknown schema version, unknown checkpoint, or an
// identity that does not match its row and this store's installation —
// is ErrCorruptDatabase or ErrInstallIDMismatch: corrupt data is rejected,
// never treated as empty, never silently reset. It is a read-only path
// that performs no write and takes no write lock; it deliberately does
// NOT require the operation to still be current (reconciliation of a
// superseded operation may need to read its journal).
func (s *Store) LoadActivationJournal(ctx context.Context, opID int64) (ActivationJournal, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	if opID <= 0 {
		return ActivationJournal{}, 0, fmt.Errorf("%w: operation id must be positive", ErrInvalidRecord)
	}
	var rowInstall, payload string
	var revision int64
	err := s.db.QueryRowContext(ctx,
		`SELECT install_id, revision, journal_json FROM activation_journals WHERE op_id = ?`, opID).Scan(&rowInstall, &revision, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return ActivationJournal{}, 0, fmt.Errorf("%w: activation journal %d", ErrRecordNotFound, opID)
	}
	if err != nil {
		return ActivationJournal{}, 0, fmt.Errorf("state: load activation journal: %w", err)
	}
	if rowInstall != s.installID {
		return ActivationJournal{}, 0, fmt.Errorf("%w: journal row names installation %q, store is %q", ErrInstallIDMismatch, rowInstall, s.installID)
	}
	if revision < 1 {
		return ActivationJournal{}, 0, ErrCorruptDatabase
	}
	j, err := decodeActivationJournal([]byte(payload))
	if err != nil {
		return ActivationJournal{}, 0, fmt.Errorf("%w: activation journal %d: %v", ErrCorruptDatabase, opID, err)
	}
	if j.InstallID != s.installID {
		return ActivationJournal{}, 0, fmt.Errorf("%w: activation journal %d is bound to a different installation", ErrInstallIDMismatch, opID)
	}
	if j.OpID != opID {
		return ActivationJournal{}, 0, fmt.Errorf("%w: activation journal %d names operation %d", ErrCorruptDatabase, opID, j.OpID)
	}
	return j, revision, nil
}

// validateActivationJournal performs the full structural validation of a
// journal against the store's installation before any write: exact schema
// version, positive operation ID, installation binding, bounded valid
// target version, a RELATIVE staged-directory identifier, a lowercase-hex
// SHA-256 verified artifact, an explicit (never guessed) prior-selection
// block, and a known checkpoint.
func validateActivationJournal(j ActivationJournal, installID string) error {
	if j.SchemaVersion != ActivationJournalSchemaVersion {
		return fmt.Errorf("%w: schema version %d, want %d", ErrInvalidRecord, j.SchemaVersion, ActivationJournalSchemaVersion)
	}
	if j.InstallID != installID {
		return fmt.Errorf("%w: journal names installation %q, store is %q", ErrInstallIDMismatch, j.InstallID, installID)
	}
	if j.OpID <= 0 {
		return fmt.Errorf("%w: operation id must be positive", ErrInvalidRecord)
	}
	if err := validateVersionString(j.TargetVersion); err != nil {
		return fmt.Errorf("%w: target version: %v", ErrInvalidRecord, err)
	}
	if err := validateStagedRelativeIdentifier(j.StagedVersionDir); err != nil {
		return err
	}
	if !isLowerHexSHA256(j.VerifiedArtifactSHA256) {
		return fmt.Errorf("%w: verified artifact digest must be a 64-character lowercase hex SHA-256", ErrInvalidRecord)
	}
	// The prior-selection block is explicit, never guessed. With no prior
	// version the generation and digest must be exactly zero/empty; with a
	// prior version the generation must be positive and the digest a full
	// lowercase-hex SHA-256. Every partial combination is refused.
	if j.PriorSelectedVersion == "" {
		if j.PriorSelectionGeneration != 0 || j.PriorSelectionDigest != "" {
			return fmt.Errorf("%w: prior selection without a version must be explicitly none (generation 0, empty digest)", ErrInvalidRecord)
		}
	} else {
		if err := validateVersionString(j.PriorSelectedVersion); err != nil {
			return fmt.Errorf("%w: prior selected version: %v", ErrInvalidRecord, err)
		}
		if j.PriorSelectionGeneration < 1 {
			return fmt.Errorf("%w: prior selection generation must be positive, not guessed", ErrInvalidRecord)
		}
		if !isLowerHexSHA256(j.PriorSelectionDigest) {
			return fmt.Errorf("%w: prior selection digest must be a 64-character lowercase hex SHA-256", ErrInvalidRecord)
		}
	}
	if _, ok := journalCheckpointOrder[j.Checkpoint]; !ok {
		return fmt.Errorf("%w: unknown journal checkpoint %q", ErrInvalidRecord, j.Checkpoint)
	}
	return nil
}

// validateVersionString applies the same version-string rules the
// operation record uses: valid UTF-8, bounded, nonempty after trimming,
// and free of NUL bytes.
func validateVersionString(v string) error {
	if !utf8.ValidString(v) || len(v) > maxActivationVersionBytes || strings.TrimSpace(v) == "" || strings.ContainsRune(v, 0) {
		return errors.New("empty or invalid version string")
	}
	return nil
}

// validateStagedRelativeIdentifier enforces that the staged version
// directory is a RELATIVE IDENTIFIER only: never absolute, never a
// Windows drive path, never a backslash path, never empty, `.`, or `..`
// components. The value is never opened as a path by this package.
func validateStagedRelativeIdentifier(id string) error {
	if id == "" || !utf8.ValidString(id) || len(id) > maxActivationVersionBytes || strings.ContainsRune(id, 0) || strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: staged version directory identifier is empty or invalid", ErrInvalidRecord)
	}
	if strings.HasPrefix(id, "/") || strings.ContainsRune(id, '\\') || strings.ContainsRune(id, ':') {
		return fmt.Errorf("%w: staged version directory identifier %q is not relative", ErrInvalidRecord, id)
	}
	for _, part := range strings.Split(id, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("%w: staged version directory identifier %q has an empty, dot, or parent component", ErrInvalidRecord, id)
		}
	}
	return nil
}

// isLowerHexSHA256 reports whether s is exactly 64 lowercase hexadecimal
// characters — the canonical textual SHA-256 digest this package stores.
func isLowerHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// decodeActivationJournal strictly decodes a persisted journal payload:
// bounded, valid UTF-8, exactly one JSON object, no duplicate keys, no
// unknown fields, no trailing data, then full validation. Corrupt rows are
// errors, never empty journals.
func decodeActivationJournal(data []byte) (ActivationJournal, error) {
	var j ActivationJournal
	if len(data) > maxActivationJournalJSONBytes || !utf8.Valid(data) {
		return j, ErrInvalidRecord
	}
	// Journal fields are flat canonical names: reject duplicate/case
	// aliases before encoding/json can silently pick a last value.
	scan := json.NewDecoder(bytes.NewReader(data))
	tok, err := scan.Token()
	if err != nil || tok != json.Delim('{') {
		return j, ErrInvalidRecord
	}
	seen := map[string]bool{}
	for scan.More() {
		tok, err = scan.Token()
		if err != nil {
			return j, ErrInvalidRecord
		}
		key, ok := tok.(string)
		if !ok || key == "" || key != strings.ToLower(key) || seen[key] {
			return j, ErrInvalidRecord
		}
		seen[key] = true
		var value json.RawMessage
		if err = scan.Decode(&value); err != nil {
			return j, ErrInvalidRecord
		}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&j); err != nil {
		return ActivationJournal{}, fmt.Errorf("malformed JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return ActivationJournal{}, fmt.Errorf("trailing data: %w", err)
		}
		return ActivationJournal{}, errors.New("trailing data")
	}
	if err := validateActivationJournal(j, j.InstallID); err != nil {
		return ActivationJournal{}, err
	}
	return j, nil
}
