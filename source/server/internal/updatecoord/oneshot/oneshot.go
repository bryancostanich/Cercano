// Package oneshot implements the approved ONE-SHOT update-utility execution
// boundary: a bounded, in-process slice that lets trusted compiled code
// perform exactly one update operation against a per-installation SQLite
// state store, under the installation's shared exclusion lock.
//
// Approved scope and explicit non-goals:
//
//   - This is a SHORT-LIVED UTILITY slice, not a service. There is no
//     listener, no socket/pipe control API, no daemon, and no control
//     credential or token system. The executor is called in-process by the
//     trusted host that compiled it.
//   - There is NO process-spawning wrapper, artifact download/bootstrap, or
//     main-CLI wiring here yet: those are later, separately gated phases
//     (plan Phase 5). This package only defines and enforces the execution
//     boundary and its invariants so later phases can be built on it.
//   - No new transport, manager invocation (Homebrew/APT/Chocolatey), state
//     or home-directory creation default, credential access, or agent-kill
//     behavior is added.
//
// Trust model:
//
//   - The state.Store is opened by the caller on an EXPLICIT root (tests use
//     temporary roots; nothing here discovers or opens the user's live
//     state). The Request must name the store's exact installation and the
//     CURRENT operation; anything else is refused before any work runs.
//   - The exclusive exclusion lock is acquired in the SAME stable
//     installation state directory the store itself owns
//     (Store.Directory()), never in a caller-supplied lock path. The lock
//     directory identity therefore cannot be redirected by any input.
//   - The backend callback is TRUSTED COMPILED CODE wired by the host
//     process — never instructions, paths, or commands taken from release
//     metadata or user input. It must quiesce all work it owns before it
//     returns; the boundary never terminates or manages processes for it.
//   - The backend mutates state ONLY through this package's Controller,
//     which delegates to the existing state.Adapter and its pure
//     operation.Store model. This slice adds no second state machine and
//     no new transition: every legality rule (protected regions, recovery
//     freezes, consent, health-before-cleanup) stays enforced by the
//     existing model.
//
// Context discipline:
//
//   - The context handed to Run (and to the backend) is the UTILITY
//     LIFETIME context — explicitly NOT a UI connection context. A lost or
//     disconnected UI must never decide the transaction outcome, release
//     admission, or cancel protected work.
//   - After the backend returns, the boundary re-reads and records the
//     outcome using a bounded, cancellation-INDEPENDENT context, so a
//     cancelled caller context can neither fake success nor leave a
//     protected interrupted state unrecorded. Protected interrupted states
//     remain recovery-needed; admission is never released based on a UI
//     disconnect.
//
// Outcome discipline:
//
//   - Success is returned ONLY after the adapter confirms the persisted
//     current operation is StateComplete. A nil backend error with an
//     unfinished persisted state is a typed ErrIncomplete; the boundary
//     NEVER marks the operation complete by itself.
//   - A backend error persists ONE sanitized generic failure — a fixed
//     machine code and fixed user phrase, never the backend's raw error
//     text — and ONLY where the model still permits it: terminal,
//     failed-marked, recovery-requested, and recovery-needed states are
//     preserved exactly as the backend (or a protected interruption) left
//     them.
//   - The best-effort progress reporter receives only safe fields
//     (installation, operation ID, target version, state) — never raw
//     reasons or filesystem paths — and its errors or panics can never
//     decide the transaction outcome.
package oneshot

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"cercano/source/server/internal/updatecoord/exclusion"
	"cercano/source/server/internal/updatecoord/operation"
	"cercano/source/server/internal/updatecoord/state"
)

// recordOutcomeTimeout bounds the cancellation-independent outcome
// recording window after the backend returns. Individual store operations
// are additionally capped internally by the state package.
const recordOutcomeTimeout = 15 * time.Second

// Sentinel errors. Callers match with errors.Is; none of them embeds raw
// user reasons, paths, or secrets.
var (
	// ErrInvalidRequest: the request is structurally unusable (nil store or
	// backend, or a non-positive operation ID).
	ErrInvalidRequest = errors.New("oneshot: invalid request")
	// ErrInstallMismatch: the request's installation identifier is not the
	// store's installation.
	ErrInstallMismatch = errors.New("oneshot: installation mismatch")
	// ErrExclusionUnavailable: the exclusive lock could not be acquired
	// (contention, or a cancelled/bounded context).
	ErrExclusionUnavailable = errors.New("oneshot: exclusion lock unavailable")
	// ErrStaleOperation: the request names an operation that is not the
	// installation's current operation.
	ErrStaleOperation = errors.New("oneshot: stale operation")
	// ErrTerminalOperation: the operation already reached a terminal state.
	ErrTerminalOperation = errors.New("oneshot: operation already terminal")
	// ErrDeferredOperation: the operation was deferred by the user and
	// requires an explicit resume before any backend work may run.
	ErrDeferredOperation = errors.New("oneshot: operation deferred")
	// ErrIncomplete: the backend returned nil without the persisted
	// operation being complete. The state is left exactly as the backend
	// persisted it; the boundary never completes it by itself.
	ErrIncomplete = errors.New("oneshot: backend returned without a completed operation")
	// ErrBackendFailed: the trusted backend reported failure. A sanitized
	// generic failure was recorded where the model permitted it.
	ErrBackendFailed = errors.New("oneshot: backend failed")
)

const (
	// GenericFailureCode is the sanitized machine code persisted when the
	// trusted backend fails without recording its own specific failure.
	GenericFailureCode operation.MachineCode = "execution-failed"
	// genericFailureReason is the fixed, sanitized user phrase persisted
	// alongside GenericFailureCode. The backend's raw error text is never
	// persisted.
	genericFailureReason = "the update did not complete; please try again"
)

// genericFailure is the only failure value this boundary ever writes.
var genericFailure = operation.Failure{Code: GenericFailureCode, UserReason: genericFailureReason}

// Request names the exact installation and the exact current operation the
// caller intends to execute. Both fields are REQUIRED to match the store:
// the installation identifier must equal the store's, and the operation ID
// must be the current operation re-read AFTER the exclusive lock is held.
// No field carries commands, paths, or release-provided instructions.
type Request struct {
	// InstallID is the opaque installation identifier the store is bound
	// to.
	InstallID string
	// OperationID is the current operation's persisted identifier.
	OperationID int64
}

// Progress carries ONLY safe, presentable fields. It never carries raw
// failure reasons, error text, or filesystem paths.
type Progress struct {
	// InstallID is the installation the operation belongs to.
	InstallID string
	// OperationID is the persisted operation identifier.
	OperationID int64
	// TargetVersion is the operation's target version, if recorded.
	TargetVersion string
	// State is the persisted operation state after the reported change.
	State operation.State
}

// Result reports the persisted outcome of a successful run.
type Result struct {
	// OperationID is the completed operation's identifier.
	OperationID int64
	// State is the persisted terminal state (always StateComplete for a
	// nil error).
	State operation.State
	// TargetVersion is the completed operation's target version.
	TargetVersion string
}

// Backend is the trusted, compiled, in-process callback that performs one
// operation's work under the exclusive lease. It is NOT user- or
// metadata-provided instructions: nothing in Request or Progress feeds it
// commands. It must quiesce all work it owns before returning; the boundary
// never spawns, signals, or kills processes on its behalf.
//
// The backend advances the operation exclusively through the Controller
// (the existing state.Adapter transitions), including recording its own
// specific failures (EventFail) and resolving protected-region freezes
// (EventBackendRecovered / EventBackendFailed) when it is the entity that
// owns that recovery. A nil return means the backend believes the operation
// succeeded — the boundary still refuses unless the persisted state is
// actually complete.
type Backend func(ctx context.Context, c *Controller) error

// Controller is the ONLY handle the backend receives. It fixes every
// mutation to the single operation named by the Request and delegates to
// the existing state.Adapter; it adds no state machine of its own. The
// backend cannot start a different operation, name another operation's ID,
// or bypass the model's legality rules.
type Controller struct {
	executor *Executor
	adapter  *state.Adapter
	opID     int64
}

// OperationID returns the operation this controller is bound to.
func (c *Controller) OperationID() int64 {
	return c.opID
}

// Snapshot returns the operation's current persisted snapshot as the
// adapter sees it.
func (c *Controller) Snapshot(ctx context.Context) (operation.Snapshot, bool, error) {
	return c.adapter.Snapshot(ctx)
}

// Apply advances the bound operation by one legal model transition. The
// operation ID is forced to the bound operation; the adapter and the pure
// model enforce every legality rule (stale check, protected regions,
// recovery freezes, consent, ordering). A best-effort progress report with
// only safe fields follows every successful transition.
func (c *Controller) Apply(ctx context.Context, in operation.Input) (operation.Snapshot, error) {
	in.OperationID = c.opID
	snap, err := c.adapter.Apply(ctx, in)
	if err != nil {
		return snap, err
	}
	c.executor.report(snap)
	return snap, nil
}

// Executor is the one-shot execution boundary for one installation. It is
// small and single-purpose: validate the request, hold the exclusive lock,
// re-read the current operation, run the one trusted backend callback, and
// judge the outcome strictly from the persisted state.
type Executor struct {
	store   *state.Store
	backend Backend

	progress func(Progress) error
	broken   atomic.Bool
}

// New creates an executor over the caller-opened trusted store and the
// trusted compiled backend callback. The store must have been Opened on an
// explicit root by the caller; this constructor never opens state itself.
func New(store *state.Store, backend Backend) (*Executor, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: nil store", ErrInvalidRequest)
	}
	if backend == nil {
		return nil, fmt.Errorf("%w: nil backend", ErrInvalidRequest)
	}
	return &Executor{store: store, backend: backend}, nil
}

// SetProgressReporter installs an optional best-effort progress callback.
// The callback receives only safe fields. Its errors or panics disable
// further reports and can NEVER decide the transaction outcome.
func (e *Executor) SetProgressReporter(fn func(Progress) error) {
	e.progress = fn
}

// Run executes the requested operation once, under the exclusive lock.
//
// Sequence and invariants:
//
//  1. The request's installation and operation identifiers must match the
//     store; anything else is refused with no work and no callback.
//  2. The exclusive Update lock is acquired in the store's own stable state
//     directory (Store.Directory()) — never a caller-supplied path — and is
//     released on every return, success or failure.
//  3. The current operation is re-read AFTER the lock is held: a stale
//     operation ID, a terminal operation (including cancelled), or a
//     deferred operation is refused before the callback runs.
//  4. The single trusted backend callback runs under the lease with the
//     UTILITY-LIFETIME context (never a UI connection context).
//  5. The outcome is re-read and, when needed, recorded with a bounded
//     cancellation-independent context: success requires a persisted
//     StateComplete; a nil backend error with any other persisted state is
//     a typed ErrIncomplete and the state is left untouched; a backend
//     error persists one sanitized generic failure ONLY where the model
//     still permits it, preserving terminal, failed-marked, and
//     recovery-flagged states exactly as they were.
func (e *Executor) Run(ctx context.Context, req Request) (Result, error) {
	if req.InstallID == "" || req.InstallID != e.store.InstallID() {
		return Result{}, fmt.Errorf("%w: request names %q, store owns %q",
			ErrInstallMismatch, req.InstallID, e.store.InstallID())
	}
	if req.OperationID <= 0 {
		return Result{}, fmt.Errorf("%w: operation id must be positive", ErrInvalidRequest)
	}

	adapter := state.NewAdapter(e.store)

	// Exclusive lease in the store's OWN directory. A caller never supplies
	// a lock path. Context errors here (including a UI-disconnect-triggered
	// cancel) simply mean no work ran.
	handle, err := exclusion.Acquire(ctx, e.store.Directory(), exclusion.Update)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrExclusionUnavailable, err)
	}
	// Release the lease on every return path, normal or error. A panic in
	// the trusted backend also unwinds through this defer.
	defer handle.Close() //nolint:errcheck // best-effort release; OS releases on exit regardless

	// Re-read the current snapshot AFTER the lease is held, so the decision
	// reflects the state serialized behind the lock.
	snap, ok, err := adapter.Snapshot(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("oneshot: re-reading current operation: %w", err)
	}
	if !ok {
		return Result{}, fmt.Errorf("%w: no current operation", ErrStaleOperation)
	}
	if snap.ID != req.OperationID {
		return Result{}, fmt.Errorf("%w: request names operation %d, current operation is %d",
			ErrStaleOperation, req.OperationID, snap.ID)
	}
	if snap.State.Terminal() {
		return Result{}, fmt.Errorf("%w: operation %d is %s", ErrTerminalOperation, req.OperationID, snap.State)
	}
	if snap.State == operation.StateDeferred {
		return Result{}, fmt.Errorf("%w: operation %d requires an explicit resume", ErrDeferredOperation, req.OperationID)
	}

	e.report(snap)

	ctrl := &Controller{executor: e, adapter: adapter, opID: req.OperationID}
	backendErr := e.backend(ctx, ctrl)

	// Judge and (when needed) record the outcome with a bounded,
	// cancellation-INDEPENDENT context: a cancelled caller context can
	// neither fake success nor leave a protected interrupted state
	// unrecorded.
	outcomeCtx, cancel := context.WithTimeout(context.Background(), recordOutcomeTimeout)
	defer cancel()

	final, ok, err := adapter.Snapshot(outcomeCtx)
	if err != nil {
		return Result{}, fmt.Errorf("oneshot: reading recorded outcome: %w", err)
	}
	if !ok {
		return Result{}, fmt.Errorf("%w: current operation vanished while the backend ran", ErrStaleOperation)
	}

	if backendErr != nil {
		recordGenericFailure(outcomeCtx, adapter, final)
		return Result{}, fmt.Errorf("%w: %w", ErrBackendFailed, backendErr)
	}
	if final.ID == req.OperationID && final.State == operation.StateComplete {
		return Result{
			OperationID:   final.ID,
			State:         final.State,
			TargetVersion: final.TargetVersion,
		}, nil
	}
	// Nil backend error, unfinished persisted state: refuse. The state stays
	// exactly as the backend persisted it; this boundary never completes an
	// operation by itself.
	return Result{}, fmt.Errorf("%w: persisted state is %s", ErrIncomplete, final.State)
}

// recordGenericFailure persists the single sanitized generic failure, but
// ONLY where the model still permits it. Terminal operations, operations
// that already carry a recorded failure, and protected interrupted states
// (recovery-requested or recovery-needed) are preserved exactly: the
// boundary never overwrites the backend's own or an interruption's recorded
// outcome, and a refused recording leaves the state untouched.
func recordGenericFailure(ctx context.Context, adapter *state.Adapter, snap operation.Snapshot) {
	if snap.State.Terminal() || snap.HasFailure || snap.RecoveryRequested || snap.RecoveryNeeded {
		return
	}
	_, err := adapter.Apply(ctx, operation.Input{
		Event:       operation.EventFail,
		OperationID:  snap.ID,
		Failure:      genericFailure,
	})
	_ = err // an illegal recording attempt is skipped, preserving the state
}

// report delivers one best-effort progress report of safe fields only. A
// panicking or erroring reporter disables further reports and can never
// decide the transaction outcome.
func (e *Executor) report(snap operation.Snapshot) {
	fn := e.progress
	if fn == nil || e.broken.Load() {
		return
	}
	defer func() {
		if recover() != nil {
			e.broken.Store(true)
		}
	}()
	if err := fn(Progress{
		InstallID:     e.store.InstallID(),
		OperationID:    snap.ID,
		TargetVersion: snap.TargetVersion,
		State:         snap.State,
	}); err != nil {
		e.broken.Store(true)
	}
}
