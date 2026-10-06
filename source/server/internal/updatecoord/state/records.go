package state

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

	"cercano/source/server/internal/updatecoord/operation"
	"cercano/source/server/internal/updatecoord/policy"
)

// knownStates is the set of operation lifecycle states the canonical
// operation.Record may carry. A record with any other state string is
// rejected rather than stored or returned (basic status validation; the
// full transition legality lives in the operation package, not here).
var knownStates = map[operation.State]bool{
	operation.StateChecking:    true,
	operation.StateAnnounced:   true,
	operation.StateReady:       true,
	operation.StateDownloading: true,
	operation.StateVerifying:   true,
	operation.StateWaiting:     true,
	operation.StateDraining:    true,
	operation.StateInstalling:  true,
	operation.StateRestarting:  true,
	operation.StateHealthCheck: true,
	operation.StateCleanup:     true,
	operation.StateComplete:    true,
	operation.StateDeferred:    true,
	operation.StateCancelled:   true,
	operation.StateFailed:      true,
	operation.StateRecovered:   true,
}

// maxReasonBytes bounds the stored failure user reason so a malformed or
// hostile record cannot park unbounded data in the state database. The
// store never logs user reasons; this is a storage bound only.
const maxReasonBytes = 4096

// SaveOperationRecord validates rec against the canonical operation.Record
// schema and this store's installation identity, serializes it canonically,
// and persists it under a compare-and-swap on the record's revision.
//
// expectedRevision is the revision the CALLER last observed: 0 means the
// record is expected to be new (an INSERT with revision 1), any other value
// must match the stored revision exactly or the save fails with
// ErrStaleRevision and NOTHING is written — a stale copy of an older
// operation record can never overwrite a newer persisted state. The save
// (revision bump and payload) is a single transaction, so a failed or
// interrupted save leaves the previously persisted record fully intact.
//
// This is the storage primitive only: it does not run the operation model's
// Start/Apply logic. The adapter that restores a Record into the pure
// operation.Store and drives transitions through its Apply — using this CAS
// to serialize across processes — is the next slice.
func (s *Store) SaveOperationRecord(ctx context.Context, expectedRevision int64, rec operation.Record) (newRevision int64, err error) {
	if expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return 0, ErrInvalidRecord
	}
	if err := validateOperationRecord(rec, s.installID); err != nil {
		return 0, err
	}
	// Canonical JSON: encoding/json marshals struct fields in declaration
	// order, so the persisted bytes are deterministic for a given record.
	payload, err := json.Marshal(rec)
	if err != nil {
		return 0, fmt.Errorf("%w: marshal: %v", ErrInvalidRecord, err)
	}

	err = s.inWriteTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		rev, err := s.saveOperationRecordConn(ctx, conn, expectedRevision, rec, payload)
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

// saveOperationRecordConn is the conn-scoped record-write primitive. It
// MUST be called inside a caller-owned BEGIN IMMEDIATE transaction: the
// operation adapter composes counter allocation, active-operation selection,
// and the revisioned record write into ONE transaction instead of nesting
// the exported per-operation helpers (each opens its own transaction).
//
// The allocation check (record ID strictly below the persisted counter) and
// the revision compare-and-swap run in the same transaction as the write,
// so an unallocated ID or a stale revision can never commit and a failed
// composite transaction rolls back with everything else.
func (s *Store) saveOperationRecordConn(ctx context.Context, conn *sql.Conn, expectedRevision int64, rec operation.Record, payload []byte) (int64, error) {
	var next int64
	if err := conn.QueryRowContext(ctx, `SELECT next_op_id FROM install_state WHERE install_id=?`, s.installID).Scan(&next); err != nil {
		return 0, ErrCorruptDatabase
	}
	if rec.ID >= next {
		return 0, fmt.Errorf("%w: operation ID was not allocated", ErrInvalidRecord)
	}
	if s.fault != nil {
		if ferr := s.fault(); ferr != nil {
			return 0, ferr
		}
	}
	if expectedRevision == 0 {
		var count int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM operation_records WHERE op_id=?`, rec.ID).Scan(&count); err != nil {
			return 0, err
		}
		if count != 0 {
			return 0, ErrStaleRevision
		}
		_, err := conn.ExecContext(ctx,
			`INSERT INTO operation_records (op_id, install_id, revision, record_json) VALUES (?, ?, 1, ?)`,
			rec.ID, rec.InstallationID, string(payload))
		if err != nil {
			return 0, fmt.Errorf("state: insert operation record: %w", err)
		}
		return 1, nil
	}
	res, err := conn.ExecContext(ctx,
		`UPDATE operation_records SET revision = revision + 1, record_json = ? WHERE op_id = ? AND install_id = ? AND revision = ?`,
		string(payload), rec.ID, rec.InstallationID, expectedRevision)
	if err != nil {
		return 0, fmt.Errorf("state: update operation record: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("state: update operation record: %w", err)
	}
	if n == 0 {
		return 0, fmt.Errorf("%w: operation record %d is not at revision %d", ErrStaleRevision, rec.ID, expectedRevision)
	}
	return expectedRevision + 1, nil
}

// loadCurrentOperationConn loads the installation's CURRENT operation
// record — the record with the HIGHEST persisted operation ID; history
// below the current operation is immutable from the adapter once a newer
// operation exists — together with its revision. It reads no rows when no
// operation exists. It is safe inside a caller-owned write transaction
// (where it also enforces that the record's ID was allocated below the
// persisted counter) and outside one, so reads never need the write lock.
// A row failing strict decoding or validation is ErrCorruptDatabase.
func (s *Store) loadCurrentOperationConn(ctx context.Context, conn *sql.Conn) (operation.Record, int64, bool, error) {
	var payload string
	var opID, revision int64
	err := conn.QueryRowContext(ctx,
		`SELECT op_id, revision, record_json FROM operation_records WHERE install_id = ? ORDER BY op_id DESC LIMIT 1`,
		s.installID).Scan(&opID, &revision, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return operation.Record{}, 0, false, nil
	}
	if err != nil {
		return operation.Record{}, 0, false, fmt.Errorf("state: load current operation: %w", err)
	}
	if opID <= 0 || revision < 1 {
		return operation.Record{}, 0, false, fmt.Errorf("%w: operation record %d at revision %d", ErrCorruptDatabase, opID, revision)
	}
	rec, err := decodeOperationRecord([]byte(payload))
	if err != nil {
		return operation.Record{}, 0, false, fmt.Errorf("%w: operation record %d: %v", ErrCorruptDatabase, opID, err)
	}
	if rec.ID != opID || rec.InstallationID != s.installID {
		return operation.Record{}, 0, false, fmt.Errorf("%w: operation record %d is bound to a different installation", ErrInstallIDMismatch, opID)
	}
	var next int64
	if err := conn.QueryRowContext(ctx, `SELECT next_op_id FROM install_state WHERE install_id=?`, s.installID).Scan(&next); err != nil {
		return operation.Record{}, 0, false, ErrCorruptDatabase
	}
	if rec.ID >= next {
		return operation.Record{}, 0, false, fmt.Errorf("%w: operation record %d was never allocated", ErrCorruptDatabase, opID)
	}
	return rec, revision, true, nil
}

// LoadOperationRecord loads the persisted canonical operation.Record with
// the given operation ID, together with its current revision (for the next
// compare-and-swap save). A missing record is ErrRecordNotFound. A row that
// fails strict decoding or validation — malformed JSON, unknown fields,
// wrong schema version, unknown state, or identifiers that do not match the
// row and this store's installation — is ErrCorruptDatabase: corrupt data is
// rejected, never treated as empty and never silently re-created.
func (s *Store) LoadOperationRecord(ctx context.Context, opID int64) (operation.Record, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	var payload string
	var revision int64
	err := s.db.QueryRowContext(ctx,
		`SELECT record_json, revision FROM operation_records WHERE op_id = ? AND install_id = ?`,
		opID, s.installID).Scan(&payload, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return operation.Record{}, 0, fmt.Errorf("%w: operation record %d", ErrRecordNotFound, opID)
	}
	if err != nil {
		return operation.Record{}, 0, fmt.Errorf("state: load operation record: %w", err)
	}
	if revision < 1 {
		return operation.Record{}, 0, ErrCorruptDatabase
	}
	rec, err := decodeOperationRecord([]byte(payload))
	if err != nil {
		return operation.Record{}, 0, fmt.Errorf("%w: operation record %d: %v", ErrCorruptDatabase, opID, err)
	}
	if rec.ID != opID || rec.InstallationID != s.installID {
		return operation.Record{}, 0, fmt.Errorf("%w: operation record %d is bound to a different installation", ErrInstallIDMismatch, opID)
	}
	return rec, revision, nil
}

// SavePolicyRecord validates and strictly encodes the delegation record and
// persists it for this installation under a compare-and-swap on the policy
// revision. The record's InstallID MUST match this store's installation
// (the strict encode/decode boundary never crosses installations); a
// mismatch is refused with ErrInstallIDMismatch before any write. The same
// revision rules as SaveOperationRecord apply: expectedRevision 0 inserts at
// revision 1, any other value must match or the save refuses with
// ErrStaleRevision without writing. The persisted record is DATA: it grants
// no execution authority by itself; only policy.EvaluateDelegation against
// distinct current evidence can produce a positive decision.
func (s *Store) SavePolicyRecord(ctx context.Context, expectedRevision int64, rec policy.DelegationRecord) (int64, error) {
	if expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return 0, ErrInvalidRecord
	}
	if rec.InstallID != s.installID {
		return 0, fmt.Errorf("%w: record names installation %q, store is %q", ErrInstallIDMismatch, rec.InstallID, s.installID)
	}
	// Strict encode: structural validation happens before any write. An
	// unset schema version is stamped current; an unknown nonzero one is
	// refused rather than coerced.
	payload, err := rec.Encode()
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidRecord, err)
	}

	if _, err := policy.DecodeDelegationRecord(payload); err != nil {
		return 0, fmt.Errorf("%w: invalid policy encoding", ErrInvalidRecord)
	}
	var newRevision int64
	err = s.inWriteTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		if s.fault != nil {
			if ferr := s.fault(); ferr != nil {
				return ferr
			}
		}
		if expectedRevision == 0 {
			var count int
			if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM policy_records WHERE install_id=?`, s.installID).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return ErrStaleRevision
			}
			_, err := conn.ExecContext(ctx,
				`INSERT INTO policy_records (install_id, revision, record_json) VALUES (?, 1, ?)`,
				s.installID, string(payload))
			if err != nil {
				return fmt.Errorf("state: insert policy record: %w", err)
			}
			newRevision = 1
			return nil
		}
		res, err := conn.ExecContext(ctx,
			`UPDATE policy_records SET revision = revision + 1, record_json = ? WHERE install_id = ? AND revision = ?`,
			string(payload), s.installID, expectedRevision)
		if err != nil {
			return fmt.Errorf("state: update policy record: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("state: update policy record: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("%w: policy record is not at revision %d", ErrStaleRevision, expectedRevision)
		}
		newRevision = expectedRevision + 1
		return nil
	})
	if err != nil {
		return 0, err
	}
	return newRevision, nil
}

// LoadPolicyRecord loads this installation's persisted delegation record
// and its current revision. The payload is decoded through the STRICT
// policy boundary (unknown fields, duplicate keys, unknown schema versions,
// trailing data, and invalid values are all rejected); a record whose
// InstallID does not match this store's installation is refused with
// ErrInstallIDMismatch rather than returned. Missing is ErrRecordNotFound;
// malformed is ErrCorruptDatabase and is never treated as empty.
func (s *Store) LoadPolicyRecord(ctx context.Context) (policy.DelegationRecord, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	var payload string
	var revision int64
	err := s.db.QueryRowContext(ctx,
		`SELECT record_json, revision FROM policy_records WHERE install_id = ?`,
		s.installID).Scan(&payload, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return policy.DelegationRecord{}, 0, fmt.Errorf("%w: policy record", ErrRecordNotFound)
	}
	if err != nil {
		return policy.DelegationRecord{}, 0, fmt.Errorf("state: load policy record: %w", err)
	}
	if revision < 1 {
		return policy.DelegationRecord{}, 0, ErrCorruptDatabase
	}
	rec, err := policy.DecodeDelegationRecord([]byte(payload))
	if err != nil {
		return policy.DelegationRecord{}, 0, fmt.Errorf("%w: policy record: %v", ErrCorruptDatabase, err)
	}
	if rec.InstallID != s.installID {
		return policy.DelegationRecord{}, 0, fmt.Errorf("%w: policy record names installation %q, store is %q", ErrInstallIDMismatch, rec.InstallID, s.installID)
	}
	return rec, revision, nil
}

// validateOperationRecord performs the basic schema/status validation of a
// canonical operation record against the store's installation before any
// write: exact schema version, positive operation ID, installation binding,
// nonempty target version, a known lifecycle state, timestamps present, and
// a failure block that is complete and bounded when present. The full
// transition legality remains in the operation package.
func validateOperationRecord(rec operation.Record, installID string) error {
	if rec.SchemaVersion != operation.RecordSchemaVersion {
		return fmt.Errorf("%w: schema version %d, want %d", ErrInvalidRecord, rec.SchemaVersion, operation.RecordSchemaVersion)
	}
	if rec.ID <= 0 {
		return fmt.Errorf("%w: operation id must be positive", ErrInvalidRecord)
	}
	if rec.InstallationID != installID {
		return fmt.Errorf("%w: record names installation %q, store is %q", ErrInstallIDMismatch, rec.InstallationID, installID)
	}
	if !utf8.ValidString(rec.TargetVersion) || len(rec.TargetVersion) > 256 || strings.TrimSpace(rec.TargetVersion) == "" || strings.ContainsRune(rec.TargetVersion, 0) {
		return fmt.Errorf("%w: empty or invalid target version", ErrInvalidRecord)
	}
	if !knownStates[rec.State] {
		return fmt.Errorf("%w: unknown operation state %q", ErrInvalidRecord, rec.State)
	}
	if rec.CreatedAt.IsZero() || rec.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: missing timestamps", ErrInvalidRecord)
	}
	if rec.UpdatedAt.Before(rec.CreatedAt) {
		return fmt.Errorf("%w: updated_at before created_at", ErrInvalidRecord)
	}
	// A failure must be complete and bounded. UserReason is the model's
	// ordinary user-facing string (the CALLER composes and sanitizes it);
	// this store neither logs it nor accepts an unbounded one.
	if rec.FailureCode != "" || rec.FailureUserReason != "" {
		if rec.FailureCode == "" || rec.FailureUserReason == "" {
			return fmt.Errorf("%w: incomplete failure record", ErrInvalidRecord)
		}
		if len(rec.FailureCode) > 128 || !utf8.ValidString(rec.FailureUserReason) || len(rec.FailureUserReason) > maxReasonBytes {
			return fmt.Errorf("%w: failure reason exceeds %d bytes", ErrInvalidRecord, maxReasonBytes)
		}
	}
	return nil
}

// decodeOperationRecord strictly decodes a persisted operation record:
// exactly one JSON value, no unknown fields, then full validation. Corrupt
// rows are errors, never empty records.
func decodeOperationRecord(data []byte) (operation.Record, error) {
	var rec operation.Record
	if len(data) > 32*1024 || !utf8.Valid(data) {
		return rec, ErrInvalidRecord
	}
	// Record fields are flat canonical names: reject duplicate/case aliases
	// before encoding/json can silently pick a last value.
	scan := json.NewDecoder(bytes.NewReader(data))
	tok, err := scan.Token()
	if err != nil || tok != json.Delim('{') {
		return rec, ErrInvalidRecord
	}
	seen := map[string]bool{}
	for scan.More() {
		tok, err = scan.Token()
		if err != nil {
			return rec, ErrInvalidRecord
		}
		key, ok := tok.(string)
		if !ok || key == "" || key != strings.ToLower(key) || seen[key] {
			return rec, ErrInvalidRecord
		}
		seen[key] = true
		var value json.RawMessage
		if err = scan.Decode(&value); err != nil {
			return rec, ErrInvalidRecord
		}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rec); err != nil {
		return operation.Record{}, fmt.Errorf("malformed JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return operation.Record{}, fmt.Errorf("trailing data: %w", err)
		}
		return operation.Record{}, errors.New("trailing data")
	}
	if err := validateOperationRecord(rec, rec.InstallationID); err != nil {
		return operation.Record{}, err
	}
	return rec, nil
}
