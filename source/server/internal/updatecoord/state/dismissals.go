package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	"cercano/source/server/internal/updatecoord/policy"
)

// maxDismissalJSONBytes mirrors the strict policy codec's serialization
// bound (1 MiB): the same limit that gates DecodeDismissalRecord gates the
// bytes this store persists, so a record can never be written that the
// strict decoder would refuse for size alone.
const maxDismissalJSONBytes = 1024 * 1024

// Durable per-version dismissals.
//
// Each dismissal is one row in the dedicated dismissal_records table,
// addressed by this installation plus the exact announcement key it
// dismisses (channel, source, version) and carrying a per-entry revision
// with compare-and-save semantics plus the STRICT policy.DismissalRecord
// JSON produced by policy.Dismissal.Encode. Loading uses
// policy.DecodeDismissalRecord — unknown fields, duplicate keys, trailing
// data, wrong schema versions, and invalid UTF-8 are refused (fail closed
// as ErrCorruptDatabase), and every entry is verified to be bound to THIS
// installation across the row key and the decoded record, so a record from
// a different installation is refused even if it sits in this database.
//
// Clearing a dismissed version removes that one owned row under the same
// revision compare-and-save as the save path — a stale revision is refused
// and the caller reloads and retries. Clearing NEVER deletes or rewrites
// any file this package does not own (the database file itself is never
// removed): the cleared state is the well-defined "no entry" state, and
// loading then reports a clean not-found rather than corruption.

// SaveDismissalRecord persists one version-scoped dismissal for this
// installation. expectedRevision is the compare-and-save revision: 0 inserts
// a new entry (which must not already exist), any other value must exactly
// match the persisted revision of the entry it replaces. The record is
// serialized through the strict policy codec — which stamps the dismissal
// schema version and validates every field — and is bounded by the
// codec's 1 MiB serialization limit. A record naming any installation
// other than this store's is refused.
func (s *Store) SaveDismissalRecord(ctx context.Context, expectedRevision int64, d policy.Dismissal) (int64, error) {
	if expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return 0, fmt.Errorf("%w: invalid expected revision", ErrInvalidRecord)
	}
	if d.InstallID != s.installID {
		return 0, fmt.Errorf("%w: dismissal names installation %q", ErrInstallIDMismatch, d.InstallID)
	}
	payload, err := d.Encode()
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidRecord, err)
	}
	if len(payload) > maxDismissalJSONBytes {
		return 0, fmt.Errorf("%w: serialized record exceeds the policy 1 MiB limit", ErrInvalidRecord)
	}
	var revision int64
	err = s.inWriteTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		next, err := s.saveDismissalRecordConn(ctx, conn, expectedRevision, d, payload)
		if err != nil {
			return err
		}
		revision = next
		return nil
	})
	if err != nil {
		return 0, err
	}
	return revision, nil
}

// saveDismissalRecordConn is the conn-scoped primitive; it MUST run inside
// a caller-owned BEGIN IMMEDIATE transaction (the exported helper opens
// one; composite future callers may share one).
func (s *Store) saveDismissalRecordConn(ctx context.Context, conn *sql.Conn, expectedRevision int64, d policy.Dismissal, payload []byte) (int64, error) {
	if s.fault != nil {
		if err := s.fault(); err != nil {
			return 0, err
		}
	}
	if expectedRevision == 0 {
		var n int
		err := conn.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM dismissal_records WHERE install_id=? AND channel=? AND source=? AND version=?`,
			s.installID, d.Channel, d.Source, d.Version).Scan(&n)
		if err != nil {
			return 0, fmt.Errorf("%w: %v", ErrCorruptDatabase, err)
		}
		if n != 0 {
			return 0, ErrStaleRevision
		}
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO dismissal_records (install_id, channel, source, version, revision, record_json) VALUES (?,?,?,?,1,?)`,
			s.installID, d.Channel, d.Source, d.Version, string(payload)); err != nil {
			return 0, fmt.Errorf("state: insert dismissal record: %w", err)
		}
		return 1, nil
	}
	res, err := conn.ExecContext(ctx,
		`UPDATE dismissal_records SET revision=revision+1, record_json=? WHERE install_id=? AND channel=? AND source=? AND version=? AND revision=?`,
		string(payload), s.installID, d.Channel, d.Source, d.Version, expectedRevision)
	if err != nil {
		return 0, fmt.Errorf("state: update dismissal record: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("state: update dismissal record: %w", err)
	}
	if n != 1 {
		// Zero rows matched: the entry does not exist or its revision moved
		// on. Either way this save is stale; the caller reloads and retries.
		return 0, ErrStaleRevision
	}
	return expectedRevision + 1, nil
}

// LoadDismissalRecord returns the durable dismissal bound to this
// installation for the exact channel/source/version key, along with its
// current revision for a later compare-and-save. A missing entry is a
// clean typed ErrRecordNotFound; a malformed raw row — or a record that
// fails the strict policy decoder or names another installation — fails
// closed as ErrCorruptDatabase/ErrInstallIDMismatch and is never treated
// as absent or reset.
func (s *Store) LoadDismissalRecord(ctx context.Context, key policy.Dismissal) (policy.Dismissal, int64, error) {
	if _, err := key.Encode(); err != nil {
		return policy.Dismissal{}, 0, fmt.Errorf("%w: %v", ErrInvalidRecord, err)
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return policy.Dismissal{}, 0, err
	}
	defer conn.Close() //nolint:errcheck // read-only path

	var revision int64
	var payload string
	err = conn.QueryRowContext(ctx,
		`SELECT revision, record_json FROM dismissal_records WHERE install_id=? AND channel=? AND source=? AND version=?`,
		s.installID, key.Channel, key.Source, key.Version).Scan(&revision, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return policy.Dismissal{}, 0, ErrRecordNotFound
	}
	if err != nil {
		return policy.Dismissal{}, 0, fmt.Errorf("%w: %v", ErrCorruptDatabase, err)
	}
	rec, err := s.decodeDismissalRow(key.Channel, key.Source, key.Version, revision, payload)
	if err != nil {
		return policy.Dismissal{}, 0, err
	}
	return rec, revision, nil
}

// ListDismissalRecords returns every durable dismissal bound to this
// installation in channel/source/version order. An installation with no
// dismissals has an empty (valid) list. Every raw row is strict-decoded
// and binding-checked; a single malformed row fails the whole load closed
// rather than being skipped or reset.
func (s *Store) ListDismissalRecords(ctx context.Context) ([]policy.Dismissal, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close() //nolint:errcheck // read-only path

	rows, err := conn.QueryContext(ctx,
		`SELECT channel, source, version, revision, record_json FROM dismissal_records WHERE install_id=? ORDER BY channel, source, version`,
		s.installID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptDatabase, err)
	}
	defer rows.Close()
	out := []policy.Dismissal{}
	for rows.Next() {
		var channel, source, version, payload string
		var revision int64
		if err := rows.Scan(&channel, &source, &version, &revision, &payload); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCorruptDatabase, err)
		}
		rec, err := s.decodeDismissalRow(channel, source, version, revision, payload)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptDatabase, err)
	}
	return out, nil
}

// ClearDismissalRecord removes one dismissed version's durable record for
// this installation. The removal is a revision compare-and-save delete of
// the single owned row: a stale revision is refused (ErrStaleRevision) and
// the caller reloads and retries; a missing entry is a clean typed
// ErrRecordNotFound. Clearing never touches any file this package does not
// own — the database file is never deleted or recreated; the cleared state
// is simply the well-defined "no entry" state.
func (s *Store) ClearDismissalRecord(ctx context.Context, key policy.Dismissal, expectedRevision int64) error {
	if expectedRevision < 1 || expectedRevision == math.MaxInt64 {
		return fmt.Errorf("%w: invalid expected revision", ErrInvalidRecord)
	}
	if _, err := key.Encode(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRecord, err)
	}
	return s.inWriteTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		if s.fault != nil {
			if err := s.fault(); err != nil {
				return err
			}
		}
		res, err := conn.ExecContext(ctx,
			`DELETE FROM dismissal_records WHERE install_id=? AND channel=? AND source=? AND version=? AND revision=?`,
			s.installID, key.Channel, key.Source, key.Version, expectedRevision)
		if err != nil {
			return fmt.Errorf("state: clear dismissal record: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("state: clear dismissal record: %w", err)
		}
		if n == 1 {
			return nil
		}
		var revision int64
		switch err := conn.QueryRowContext(ctx,
			`SELECT revision FROM dismissal_records WHERE install_id=? AND channel=? AND source=? AND version=?`,
			s.installID, key.Channel, key.Source, key.Version).Scan(&revision); {
		case errors.Is(err, sql.ErrNoRows):
			return ErrRecordNotFound
		case err != nil:
			return fmt.Errorf("%w: %v", ErrCorruptDatabase, err)
		default:
			// The entry still exists with a different revision: this clear
			// is stale; the caller reloads and retries.
			return ErrStaleRevision
		}
	})
}

// decodeDismissalRow strict-decodes one persisted dismissal payload and
// verifies it against its row: exact row-key match, positive revision,
// the policy codec's 1 MiB limit, valid UTF-8, and binding to THIS
// installation. Any violation fails closed.
func (s *Store) decodeDismissalRow(channel, source, version string, revision int64, payload string) (policy.Dismissal, error) {
	data := []byte(payload)
	if revision < 1 {
		return policy.Dismissal{}, fmt.Errorf("%w: non-positive revision %d", ErrCorruptDatabase, revision)
	}
	if len(data) > maxDismissalJSONBytes {
		return policy.Dismissal{}, fmt.Errorf("%w: record exceeds the policy 1 MiB limit", ErrCorruptDatabase)
	}
	if !utf8.Valid(data) {
		return policy.Dismissal{}, fmt.Errorf("%w: invalid UTF-8 record", ErrCorruptDatabase)
	}
	rec, err := policy.DecodeDismissalRecord(data)
	if err != nil {
		// Includes refusing future/unknown dismissal schema versions.
		return policy.Dismissal{}, fmt.Errorf("%w: strict decode refused record: %v", ErrCorruptDatabase, err)
	}
	if rec.InstallID != s.installID {
		return policy.Dismissal{}, fmt.Errorf("%w: record names installation %q", ErrInstallIDMismatch, rec.InstallID)
	}
	if rec.Channel != channel || rec.Source != source || rec.Version != version {
		return policy.Dismissal{}, fmt.Errorf("%w: row key does not match the record", ErrCorruptDatabase)
	}
	return rec, nil
}
