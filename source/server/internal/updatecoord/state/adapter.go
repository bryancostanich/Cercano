package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"

	"cercano/source/server/internal/updatecoord/operation"
)

// Adapter is the durable-operation adapter: the atomic bridge between the
// persisted operation records in this store and the PURE operation state
// machine in the operation package. It implements Start, Apply and
// Snapshot atomically over the existing SQLite schema (version 1, no
// migration, no schema change):
//
//   - The CURRENT operation is the record with the highest persisted
//     operation ID. Once a newer operation exists, older records are
//     history and are never mutated again by this adapter.
//   - Start performs active-operation selection, counter allocation, and
//     the new record's insert inside ONE BEGIN IMMEDIATE transaction, so
//     two handles (even in two processes) starting simultaneously observe
//     each other and allocate exactly one identifier; an aborted start
//     rolls the identifier counter back with everything else.
//   - A same-target start against an active operation is idempotent and
//     returns the current operation; a different target is refused; a
//     protected failed operation (failed during activation, unconfirmed
//     by the backend) blocks a restart until the backend resolves it.
//   - Apply requires the exact current operation ID. A stale callback that
//     outlives its operation — including across a reopen/restart that
//     started a replacement operation — is refused and mutates nothing.
//     Every event is applied by restoring the persisted record into a pure
//     operation.Store and calling its Apply, so the transition table,
//     guards, and protected-region rules exist exactly once and are never
//     duplicated here. The read, the pure transition, and the
//     revision-bumped write share one BEGIN IMMEDIATE transaction, which
//     serializes same-ID events across processes; the persisted revision
//     is updated with the event in that same transaction. A refused
//     transition writes nothing: no record change and no counter change.
//
// The adapter deliberately does NOT compose the exported per-operation
// write helpers (AllocateOperationID, SaveOperationRecord): each opens its
// own BEGIN IMMEDIATE transaction, and nesting transactions is an error.
// It uses the conn-scoped internals instead.
//
// Scope: this adapter owns durable operation lifecycle state only. It has
// no notion of user-state location, runtime data, probes, RPC or UI
// integration, secrets, or push services, and it does not wire durable
// per-version dismissal.
type Adapter struct {
	store *Store
}

// NewAdapter returns the durable-operation adapter over an open store.
// The adapter keeps no separate in-memory model state: every call restores
// the pure model from the validated persisted record inside the
// transaction that performs the mutation, so this process's view can
// never drift from the database.
func NewAdapter(s *Store) *Adapter {
	return &Adapter{store: s}
}

// Start selects the installation's current operation against targetVersion
// and returns the current or newly created snapshot.
//
// In one BEGIN IMMEDIATE transaction: an active current operation with the
// same target is returned as-is (no write, no identifier burned); an
// active current operation with a different target is refused
// (MachineConflictingTarget); a terminal current operation that failed
// during activation with recovery still unconfirmed is refused
// (MachineRecoveryNeeded); otherwise one operation identifier is allocated
// from the persisted counter and the new checking-state record is inserted.
// The selection rules and refusal codes come from the pure model's Start —
// the model's identifier source is the SQLite counter inside this
// transaction — so the whole start commits atomically or rolls back
// completely (an aborted start burns no identifier gap).
func (a *Adapter) Start(ctx context.Context, targetVersion string) (operation.Snapshot, error) {
	var snap operation.Snapshot
	err := a.store.inWriteTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		cur, _, found, err := a.store.loadCurrentOperationConn(ctx, conn)
		if err != nil {
			return err
		}
		restored, err := restoreOperationRecord(cur, found)
		if err != nil {
			return err
		}

		var (
			currentID  int64 // persisted current operation, 0 when none exists
			allocated  int64 // SQLite counter allocation, 0 unless a new op started
			counterErr error
		)
		model := operation.NewStore(operation.WithIDSource(func() int64 {
			// The pure model calls its identifier source only after it has
			// applied the same-target/conflict/recovery selection rules, so
			// the persisted counter is bumped exactly when the model has
			// decided a new operation is legal — inside this transaction.
			id, err := a.store.allocateOperationIDConn(ctx, conn)
			if err != nil {
				counterErr = err
				// The sentinel never persists: counterErr is checked before
				// any record write and the transaction rolls back.
				return math.MaxInt64
			}
			allocated = id
			return id
		}))
		if found {
			if err := model.Restore(restored); err != nil {
				return fmt.Errorf("%w: current operation record: %v", ErrCorruptDatabase, err)
			}
			currentID = restored.ID
		}

		started, err := model.Start(a.store.installID, targetVersion)
		if err != nil {
			return err
		}
		if counterErr != nil {
			return counterErr
		}
		if found && started.ID == currentID {
			// Idempotent same-target start: the pure model returned the
			// persisted current operation unchanged. Nothing is written.
			snap = started
			return nil
		}
		if started.ID != allocated {
			// A new operation must carry exactly the identifier the
			// transaction allocated; anything else cannot have been
			// produced by this composition.
			return fmt.Errorf("%w: started operation %d does not match the allocated identifier %d", ErrCorruptDatabase, started.ID, allocated)
		}
		if err := a.persist(ctx, conn, 0, started); err != nil {
			return err
		}
		snap = started
		return nil
	})
	if err != nil {
		return operation.Snapshot{}, err
	}
	return snap, nil
}

// Apply applies one explicit operation event to the installation's current
// operation and returns the updated snapshot.
//
// The current record is loaded, validated, and restored into a pure
// operation.Store — whose Apply runs the actual transition, guards, and
// protected-region rules — and the resulting record replaces the persisted
// one with its revision bumped, all inside ONE BEGIN IMMEDIATE transaction.
// The write lock serializes same-ID events across processes and handles;
// the revision compare-and-swap (read and written in the same transaction)
// is the belt-and-braces second barrier.
//
// Every mutation names the exact current operation ID: a zero ID
// (MachineOperationIDRequired), an older operation's ID
// (MachineStaleOperation — a stale callback from a superseded operation,
// including across a reopen, cannot mutate the replacement), or a missing
// operation (MachineMissingOperation) are refused before any write. An
// illegal transition or guard violation is refused by the pure model with
// the transaction writing NOTHING: no record change and no counter change.
func (a *Adapter) Apply(ctx context.Context, in operation.Input) (operation.Snapshot, error) {
	var snap operation.Snapshot
	err := a.store.inWriteTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		cur, revision, found, err := a.store.loadCurrentOperationConn(ctx, conn)
		if err != nil {
			return err
		}
		restored, err := restoreOperationRecord(cur, found)
		if err != nil {
			return err
		}
		model := operation.NewStore()
		if found {
			if err := model.Restore(restored); err != nil {
				return fmt.Errorf("%w: current operation record: %v", ErrCorruptDatabase, err)
			}
		}
		next, err := model.Apply(a.store.installID, in)
		if err != nil {
			// Refused transition: the pure model mutated nothing and this
			// transaction has written nothing, so neither the record nor
			// the counter changes.
			return err
		}
		if err := a.persist(ctx, conn, revision, next); err != nil {
			return err
		}
		snap = next
		return nil
	})
	if err != nil {
		return operation.Snapshot{}, err
	}
	return snap, nil
}

// Snapshot returns the installation's current persisted operation — the
// record with the highest operation ID — as a validated snapshot, or
// ok=false when no operation exists. It is a read-only path: it takes no
// write lock and performs no write transaction. A record that fails the
// persisted-invariant validation is refused, never silently treated as
// absent or reset.
func (a *Adapter) Snapshot(ctx context.Context) (operation.Snapshot, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	conn, err := a.store.db.Conn(ctx)
	if err != nil {
		return operation.Snapshot{}, false, err
	}
	defer conn.Close() //nolint:errcheck // read-only path
	cur, _, found, err := a.store.loadCurrentOperationConn(ctx, conn)
	if err != nil || !found {
		return operation.Snapshot{}, false, err
	}
	snap, err := restoreOperationRecord(cur, found)
	if err != nil {
		return operation.Snapshot{}, false, err
	}
	return snap, true, nil
}

// persist validates and canonically serializes snap's record and saves it
// through the conn-scoped revisioned write inside the caller-owned
// transaction. expectedRevision 0 inserts a new record; any other value
// must match the revision read in the same transaction.
func (a *Adapter) persist(ctx context.Context, conn *sql.Conn, expectedRevision int64, snap operation.Snapshot) error {
	rec := snap.Record()
	if err := validateOperationRecord(rec, a.store.installID); err != nil {
		return err
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("%w: marshal: %v", ErrInvalidRecord, err)
	}
	if _, err := a.store.saveOperationRecordConn(ctx, conn, expectedRevision, rec, payload); err != nil {
		return err
	}
	return nil
}

// restoreOperationRecord validates the persisted current record against the
// pure model's own invariants — exact schema version, known state,
// ordered timestamps, and model flags consistent with the recorded state —
// and returns the equivalent snapshot. A record failing any invariant is
// refused as corrupt: unknown or contradictory persisted state is never
// silently reset, coerced, or treated as absent.
func restoreOperationRecord(rec operation.Record, found bool) (operation.Snapshot, error) {
	if !found {
		return operation.Snapshot{}, nil
	}
	snap, err := operation.RestoreSnapshot(rec)
	if err != nil {
		return operation.Snapshot{}, fmt.Errorf("%w: current operation record: %v", ErrCorruptDatabase, err)
	}
	return snap, nil
}
