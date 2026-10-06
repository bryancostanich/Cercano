// Package operation defines the pure, in-memory lifecycle state model for
// Cercano update operations.
//
// This slice is deliberately narrow. It is a state model only: it performs no
// filesystem access, no process control, no package-manager invocation, no
// network access, no persistence, and no admission/locking. It holds no
// security policy, no UI, and no RPC surface. Actual OS-level launch locks,
// drain admission, and durable recovery journals are Phase 4/5 concerns and
// are explicitly NOT implemented here.
//
// Keying and identity. An operation is keyed by an opaque, nonempty
// installation identifier that a trusted resolver (the installation
// classification layer) supplies later; this package never derives an
// installation identity from a path, a name, or any heuristic. Every
// operation also carries a nonempty target version. Operation identifiers
// are one monotonically increasing sequence per store: uniqueness comes from
// the counter, never from randomness, and tests may inject their own
// deterministic sequence. Every mutation names its target operation by that
// identifier explicitly; nothing keys on "the current operation" alone.
//
// Safety invariants encoded by the transition table:
//
//   - Explicit events only. Every state change is a listed legal edge; any
//     unlisted event is refused and leaves the state unchanged.
//   - Explicit operation keying. Every mutation (Apply) must name the exact
//     operation it targets by ID. A zero ID and any non-current ID are
//     refused and leave the model unchanged; there is no silent
//     default-to-current path, so a stale event from a previous (for
//     example cancelled) operation can never advance the new one.
//   - Wait for idle. A drain/activation event while active work exists is
//     refused. Waiting never auto-activates.
//   - Explicit consent only. Cancelling active work requires recorded user
//     consent. There is no deadline-based or forced work cancellation in the
//     model at all. Consent begins draining, but the work stays active
//     until its own explicit idle callback: drained refuses while work is
//     still active, and idle is legal in draining.
//   - Pre-activation cancellation is legal and models restored admission
//     with a flag (real admission is implemented later).
//   - Cancellation cannot be falsely claimed during install/restart/health
//     verification. A cancel attempt there only marks RecoveryRequested and
//     freezes all ordinary advancement — not even the normal path to
//     complete may proceed — until the backend's safe callback
//     (backend-recovered or backend-failed) resolves the state.
//   - A failure during activation (install/restart/health verification) is
//     blocked, not freely retryable: the failed operation records
//     RecoveryNeeded and Start refuses until the backend confirms a safe
//     failed state (backend-failed) or a successful rollback
//     (backend-recovered, or explicit recover). A failure before activation
//     is an ordinary terminal failure and retry stays free.
//   - Completion requires health success AND finished cleanup. The only
//     alternate completion is an explicit success-with-pending-cleanup
//     result, modeled as a dedicated field, never as a silent success.
//   - The superseded (old) version is only ever cleaned up after health
//     verification succeeded: no transition enters cleanup or complete from
//     any state that has not passed health verification.
//   - A health failure always records a fixed, sanitized machine code and
//     user reason composed inside the model, never from caller input.
//   - Recovery is explicit. recovered is a terminal outcome distinct from
//     complete; it never implies the update succeeded. No event implicitly
//     recovers an operation.
//   - Failures carry a machine-readable code and a user-facing reason.
//     Failure.UserReason is an ordinary string: the type has no magical
//     secrecy — the CALLER must sanitize it. The model only guarantees that
//     its own composed reasons (for example the health-failure reason) are
//     sanitized fixed phrases and that no field exists solely for raw
//     detail.
//
// Persistence is designed, not implemented: Record is the JSON schema for a
// future durable operation record. Nothing in this package writes any file.
package operation

import (
	"fmt"
	"sync"
	"time"
)

// State is an observable operation lifecycle state.
type State string

const (
	// StateChecking means the operation is determining whether a newer
	// version is actually installable through the installation's own
	// source. Announcements are not yet installability.
	StateChecking State = "checking"
	// StateAnnounced means a newer release was announced upstream but has
	// not been verified installable through the installed source.
	StateAnnounced State = "announced"
	// StateReady means a target version was verified installable and the
	// operation may proceed.
	StateReady State = "ready"
	// StateDownloading means the verified target is being acquired.
	StateDownloading State = "downloading"
	// StateVerifying means the acquired artifact is being verified.
	StateVerifying State = "verifying"
	// StateWaiting means verification passed and the operation is waiting
	// for active work to finish before draining.
	StateWaiting State = "waiting"
	// StateDraining means the admission barrier is up and active work is
	// finishing (or was explicitly consent-cancelled).
	StateDraining State = "draining"
	// StateInstalling means the package transaction / activation is in
	// progress. Cancellation here cannot be claimed; only backend recovery
	// callbacks resolve this state.
	StateInstalling State = "installing"
	// StateRestarting means the restarted process is being brought up.
	StateRestarting State = "restarting"
	// StateHealthCheck means the operation is verifying the expected
	// release is actually running and ready.
	StateHealthCheck State = "healthcheck"
	// StateCleanup means health verification succeeded and the superseded
	// version is being cleaned up.
	StateCleanup State = "cleanup"
	// StateComplete means health succeeded and cleanup finished (or was
	// explicitly recorded as success with pending cleanup).
	StateComplete State = "complete"
	// StateDeferred means the user chose to postpone the update. It is
	// resumable, not terminal.
	StateDeferred State = "deferred"
	// StateCancelled means the update was cancelled before activation.
	// Terminal.
	StateCancelled State = "cancelled"
	// StateFailed means the operation failed. Terminal except for explicit
	// recovery.
	StateFailed State = "failed"
	// StateRecovered means the previous complete version was explicitly
	// restored after a failure. Terminal, and never equal to success.
	StateRecovered State = "recovered"
)

// Terminal reports whether s is a terminal state.
func (s State) Terminal() bool {
	switch s {
	case StateComplete, StateCancelled, StateFailed, StateRecovered:
		return true
	}
	return false
}

// protectedFromCancel are the states in which a cancel request can never be
// honored as a cancellation: the operation is inside the package transaction,
// restart, or health verification. A cancel there is only recorded as a
// recovery request and the backend's safe callback resolves the state.
var protectedFromCancel = map[State]bool{
	StateInstalling:  true,
	StateRestarting:  true,
	StateHealthCheck: true,
}

// Event is an explicit transition request. Every legal state change is an
// event edge in transitions; anything else is refused.
type Event string

const (
	// EventAnnounce records a newer upstream release announcement.
	EventAnnounce Event = "announce"
	// EventReady records that the target version was verified installable
	// through the installation's own source.
	EventReady Event = "ready"
	// EventDownload begins acquiring the verified target.
	EventDownload Event = "download"
	// EventDownloaded records the acquired artifact (moves to verifying).
	EventDownloaded Event = "downloaded"
	// EventVerified records verification success (moves to waiting).
	EventVerified Event = "verified"
	// EventWorkActive records that active work exists (waiting holds).
	EventWorkActive Event = "work-active"
	// EventIdle records that active work has finished. It is the only
	// event that clears active work, and it is legal in waiting and in
	// draining (after a consent-cancelled work) alike.
	EventIdle Event = "idle"
	// EventDrain begins draining. Refused while active work exists:
	// waiting never auto-activates.
	EventDrain Event = "drain"
	// EventCancelActiveWork cancels active work. Requires recorded user
	// consent (Input.Consent); there is no other path to it. Consent
	// begins draining, but the work stays active until its explicit
	// EventIdle callback; EventDrained refuses until then.
	EventCancelActiveWork Event = "cancel-active-work"
	// EventDrained records draining finished (moves to installing).
	// Refused while active work is still running.
	EventDrained Event = "drained"
	// EventInstalled records the package transaction / activation
	// finished (moves to restarting).
	EventInstalled Event = "installed"
	// EventRestarted records the restarted process came up far enough to
	// begin health verification.
	EventRestarted Event = "restarted"
	// EventHealthSuccess records health verification succeeded. This is
	// the only edge into cleanup: old-version cleanup can never begin
	// before health verification.
	EventHealthSuccess Event = "health-success"
	// EventHealthFail records health verification failed.
	EventHealthFail Event = "health-fail"
	// EventCleanupDone records cleanup finished; the only ordinary edge
	// into complete.
	EventCleanupDone Event = "cleanup-done"
	// EventCleanupPending records cleanup could not finish (for example
	// locked files) and completes as explicit success-with-pending-cleanup.
	EventCleanupPending Event = "cleanup-pending"
	// EventCancel cancels the update. Legal only before activation; during
	// installing/restarting/healthcheck it instead marks RecoveryRequested
	// and waits for the backend safe callback.
	EventCancel Event = "cancel"
	// EventDefer postpones the update at the user's request.
	EventDefer Event = "defer"
	// EventResume resumes a deferred operation.
	EventResume Event = "resume"
	// EventFail records an operation failure. Requires a Failure with a
	// machine code and a user reason.
	EventFail Event = "fail"
	// EventRecover explicitly restores the previous complete version after
	// a failure. The only edge from failed to recovered.
	EventRecover Event = "recover"
	// EventBackendRecovered is the backend's safe callback reporting that
	// recovery succeeded. Legal only when RecoveryRequested is set, or
	// from failed with RecoveryNeeded (successful rollback confirmation).
	EventBackendRecovered Event = "backend-recovered"
	// EventBackendFailed is the backend's safe callback reporting the
	// protected region failed. Legal only when RecoveryRequested is set,
	// or from failed with RecoveryNeeded — where it confirms a safe
	// failed state and lifts the retry block.
	EventBackendFailed Event = "backend-failed"
)

// MachineCode is a machine-readable failure/refusal code. Codes are stable
// identifiers; they never embed raw paths, tokens, or secrets.
type MachineCode string

const (
	// MachineVerificationFailed: artifact verification failed.
	MachineVerificationFailed MachineCode = "verification-failed"
	// MachineDownloadFailed: target acquisition failed.
	MachineDownloadFailed MachineCode = "download-failed"
	// MachineHealthCheckFailed: health verification failed.
	MachineHealthCheckFailed MachineCode = "health-check-failed"
	// MachineInstallFailed: the package transaction / activation failed.
	MachineInstallFailed MachineCode = "install-failed"
	// MachineRestartFailed: the restart failed.
	MachineRestartFailed MachineCode = "restart-failed"
	// MachineCleanupFailed: cleanup failed and is pending retry.
	MachineCleanupFailed MachineCode = "cleanup-failed"
	// MachineAnnounceOnly: nothing installable was found for the source.
	MachineAnnounceOnly MachineCode = "announce-only"
	// MachineConflictingTarget: a different target was requested while an
	// operation is already active for the installation.
	MachineConflictingTarget MachineCode = "conflicting-target"
	// MachineIllegalTransition: the event is not a legal edge from the
	// current state.
	MachineIllegalTransition MachineCode = "illegal-transition"
	// MachineActiveWorkPresent: activation was requested while active work
	// exists; waiting never auto-activates.
	MachineActiveWorkPresent MachineCode = "active-work-present"
	// MachineConsentRequired: cancelling active work requires user consent.
	MachineConsentRequired MachineCode = "consent-required"
	// MachineMissingOperation: no operation exists for the installation.
	MachineMissingOperation MachineCode = "missing-operation"
	// MachineInvalidRequest: the request was malformed (empty keying, a
	// failure without code/reason, or a meaningless no-op).
	MachineInvalidRequest MachineCode = "invalid-request"
	// MachineInvalidIDSequence: an injected ID source produced a
	// non-monotonic identifier.
	MachineInvalidIDSequence MachineCode = "invalid-id-sequence"
	// MachineOperationIDRequired: the mutation did not name the operation
	// it intends to mutate; no silent default-to-current path exists.
	MachineOperationIDRequired MachineCode = "operation-id-required"
	// MachineStaleOperation: the named operation is not the installation's
	// current operation; a stale event from a previous (for example
	// cancelled) operation may never advance the new one.
	MachineStaleOperation MachineCode = "stale-operation"
	// MachineRecoveryPending: a cancellation was requested inside the
	// protected region; ordinary advancement is frozen until the backend
	// reports an explicit safe outcome.
	MachineRecoveryPending MachineCode = "recovery-pending"
	// MachineRecoveryNeeded: the previous operation failed during
	// activation and the backend has not yet confirmed a safe failed state
	// or a successful rollback, so no new operation may start.
	MachineRecoveryNeeded MachineCode = "recovery-needed"
)

// Failure is an operation failure record. It has no dedicated raw-detail
// field, but that is not secrecy: UserReason is an ordinary string and the
// CALLER must sanitize it (no secrets, tokens, or raw internal detail).
// The model's own composed reasons are fixed sanitized phrases.
type Failure struct {
	// Code is the machine-readable failure code.
	Code MachineCode
	// UserReason is a user-facing explanation. It must be composed by the
	// caller and must never contain secrets or raw internal detail.
	UserReason string
}

// Error is a model refusal. Its message carries only the machine code and
// the user reason — never raw internal detail.
type Error struct {
	Code       MachineCode
	UserReason string
}

func (e *Error) Error() string {
	return fmt.Sprintf("operation: %s: %s", e.Code, e.UserReason)
}

func failureErr(code MachineCode, userReason string) *Error {
	return &Error{Code: code, UserReason: userReason}
}

// healthFailUserReason is the fixed, sanitized user-facing reason recorded
// when health verification fails. It is composed here (never from caller
// input) so a health failure always carries a safe machine code and reason.
const healthFailUserReason = "the updated version did not come up healthy; your previous version is being restored"

// Input is an explicit transition request.
type Input struct {
	// Event is the requested transition event.
	Event Event
	// Consent records explicit user consent. It is required by, and only
	// meaningful for, EventCancelActiveWork.
	Consent bool
	// OperationID names the exact operation this mutation targets. It is
	// required on every Apply: a zero value and any value other than the
	// installation's current operation ID are refused and leave the model
	// unchanged. There is no silent default-to-current path, so a stale
	// event from a previous (for example cancelled) operation can never
	// advance the new one.
	OperationID int64
	// Failure is required by EventFail and ignored otherwise.
	Failure Failure
}

// Snapshot is an immutable, by-value copy of an operation's observable
// state. It contains no slices, maps, or pointers, so a caller that mutates
// a Snapshot can never affect the model.
type Snapshot struct {
	// ID is the operation's unique, monotonically assigned identifier.
	ID int64
	// InstallationID is the opaque installation key supplied by the
	// trusted resolver.
	InstallationID string
	// TargetVersion is the nonempty version this operation targets.
	TargetVersion string
	// State is the current lifecycle state.
	State State
	// ActiveWork records whether active work currently holds the
	// operation in waiting.
	ActiveWork bool
	// ConsentRecorded records that explicit user consent for cancelling
	// active work was given and accepted.
	ConsentRecorded bool
	// RecoveryRequested records that a cancel arrived inside the protected
	// region and the model is waiting for the backend safe callback.
	RecoveryRequested bool
	// RecoveryNeeded records that the operation failed during activation
	// (installing/restarting/healthcheck): the installation may be in a
	// partially-activated state, so a new operation may not start until
	// the backend confirms a safe failed state (backend-failed) or a
	// successful rollback (backend-recovered/recover).
	RecoveryNeeded bool
	// ResumeAdmission records that a pre-activation cancel/defer restored
	// normal admission. The actual admission mechanism is implemented in a
	// later slice; this is the model-level flag only.
	ResumeAdmission bool
	// HealthVerified records that health verification succeeded.
	// Old-version cleanup is reachable only after this is true.
	HealthVerified bool
	// SuccessWithPendingCleanup records that completion was granted with
	// cleanup still pending (explicitly, never silently).
	SuccessWithPendingCleanup bool
	// Failure is the recorded failure, when HasFailure is true.
	Failure Failure
	// HasFailure reports whether Failure is meaningful.
	HasFailure bool
	// CreatedAt and UpdatedAt are model bookkeeping timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Record is the persistence-oriented JSON projection of an operation. It is
// a design deliverable for the later durable-recovery slice only: nothing in
// this package serializes it to any file, and no migration code exists yet.
type Record struct {
	SchemaVersion             int       `json:"schema_version"`
	ID                        int64     `json:"id"`
	InstallationID            string    `json:"installation_id"`
	TargetVersion             string    `json:"target_version"`
	State                     State     `json:"state"`
	ActiveWork                bool      `json:"active_work"`
	ConsentRecorded           bool      `json:"consent_recorded"`
	RecoveryRequested         bool      `json:"recovery_requested"`
	RecoveryNeeded            bool      `json:"recovery_needed"`
	ResumeAdmission           bool      `json:"resume_admission"`
	HealthVerified            bool      `json:"health_verified"`
	SuccessWithPendingCleanup bool      `json:"success_with_pending_cleanup"`
	FailureCode               string    `json:"failure_code,omitempty"`
	FailureUserReason         string    `json:"failure_user_reason,omitempty"`
	CreatedAt                 time.Time `json:"created_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

// RecordSchemaVersion is the schema version Record currently models. It is
// 2: the recovery_needed flag was added to distinguish an ordinary terminal
// failure from a blocked protected failure.
const RecordSchemaVersion = 2

// Record projects the snapshot into the persistence record schema.
func (s Snapshot) Record() Record {
	r := Record{
		SchemaVersion:             RecordSchemaVersion,
		ID:                        s.ID,
		InstallationID:            s.InstallationID,
		TargetVersion:             s.TargetVersion,
		State:                     s.State,
		ActiveWork:                s.ActiveWork,
		ConsentRecorded:           s.ConsentRecorded,
		RecoveryRequested:         s.RecoveryRequested,
		RecoveryNeeded:            s.RecoveryNeeded,
		ResumeAdmission:           s.ResumeAdmission,
		HealthVerified:            s.HealthVerified,
		SuccessWithPendingCleanup: s.SuccessWithPendingCleanup,
		CreatedAt:                 s.CreatedAt,
		UpdatedAt:                 s.UpdatedAt,
	}
	if s.HasFailure {
		r.FailureCode = string(s.Failure.Code)
		r.FailureUserReason = s.Failure.UserReason
	}
	return r
}

// transitions is the complete legal-edge table. Every edge not listed here
// is refused and leaves the state unchanged.
var transitions = map[State]map[Event]State{
	StateChecking: {
		EventAnnounce: StateAnnounced,
		EventReady:    StateReady,
		EventCancel:   StateCancelled,
		EventDefer:    StateDeferred,
		EventFail:     StateFailed,
	},
	StateAnnounced: {
		EventReady:  StateReady,
		EventCancel: StateCancelled,
		EventDefer:  StateDeferred,
		EventFail:   StateFailed,
	},
	StateReady: {
		EventDownload: StateDownloading,
		EventCancel:   StateCancelled,
		EventDefer:    StateDeferred,
		EventFail:     StateFailed,
	},
	StateDownloading: {
		EventDownloaded: StateVerifying,
		EventCancel:     StateCancelled,
		EventFail:       StateFailed,
	},
	StateVerifying: {
		EventVerified: StateWaiting,
		EventCancel:   StateCancelled,
		EventFail:     StateFailed,
	},
	StateWaiting: {
		EventWorkActive:       StateWaiting,
		EventIdle:             StateWaiting,
		EventDrain:            StateDraining,
		EventCancelActiveWork: StateDraining,
		EventCancel:           StateCancelled,
		EventDefer:            StateDeferred,
		EventFail:             StateFailed,
	},
	StateDraining: {
		// Idle is legal in draining: consent-cancelled work stays active
		// until this explicit callback clears it.
		EventIdle:    StateDraining,
		EventDrained: StateInstalling,
		EventCancel:  StateCancelled,
		EventFail:    StateFailed,
	},
	StateInstalling: {
		EventInstalled:        StateRestarting,
		EventFail:             StateFailed,
		EventBackendRecovered: StateRecovered,
		EventBackendFailed:    StateFailed,
	},
	StateRestarting: {
		EventRestarted:        StateHealthCheck,
		EventFail:             StateFailed,
		EventBackendRecovered: StateRecovered,
		EventBackendFailed:    StateFailed,
	},
	StateHealthCheck: {
		EventHealthSuccess:    StateCleanup,
		EventHealthFail:       StateFailed,
		EventBackendRecovered: StateRecovered,
		EventBackendFailed:    StateFailed,
	},
	StateCleanup: {
		EventCleanupDone:    StateComplete,
		EventCleanupPending: StateComplete,
		EventFail:           StateFailed,
	},
	StateDeferred: {
		EventResume: StateReady,
		EventCancel: StateCancelled,
	},
	StateCancelled: {},
	StateFailed: {
		EventRecover: StateRecovered,
		// Backend callbacks are guarded in Apply: they are credible only
		// after a recorded recovery request, or (below, for a protected
		// failure) while RecoveryNeeded holds and the backend is expected
		// to confirm a safe failed state or a successful rollback.
		EventBackendRecovered: StateRecovered,
		EventBackendFailed:    StateFailed,
	},
	StateRecovered: {},
	StateComplete:  {},
}

// Option configures a Store.
type Option func(*Store)

// WithIDSource injects the operation ID source. The supplied function must
// return strictly increasing values; the store refuses any non-monotonic
// result. The default is an internal counter starting at 1: uniqueness comes
// from the sequence, never from randomness, and tests inject deterministic
// sequences.
func WithIDSource(next func() int64) Option {
	return func(s *Store) { s.nextID = next }
}

// Store is the in-memory operation store. It holds at most one current
// operation per installation: while that operation is non-terminal,
// duplicate requests for the same target are idempotent and requests for a
// conflicting target are refused. All access is guarded by a single mutex.
type Store struct {
	mu     sync.Mutex
	nextID func() int64
	lastID int64
	ops    map[string]*Snapshot
}

// NewStore creates an empty in-memory store.
func NewStore(opts ...Option) *Store {
	s := &Store{
		ops: make(map[string]*Snapshot),
	}
	s.nextID = func() int64 {
		// Monotonic by construction: allocateID performs the bookkeeping
		// and the strictly-increasing check.
		return s.lastID + 1
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// allocateID takes the next identifier from the configured source. It must
// be called with s.mu held.
func (s *Store) allocateID() (int64, error) {
	id := s.nextID()
	if id <= s.lastID {
		return 0, failureErr(MachineInvalidIDSequence,
			"the operation identifier sequence must be strictly increasing")
	}
	s.lastID = id
	return id, nil
}

// Start begins (or idempotently re-observes) the operation for an
// installation.
//
//   - installationID and targetVersion must be nonempty; the installationID
//     is the opaque key supplied by the trusted resolver.
//   - If a non-terminal operation exists with the same target, the request
//     is idempotent: the existing operation's snapshot is returned.
//   - If a non-terminal operation exists with a different target, the
//     request is refused (conflicting-target) and the existing operation is
//     unchanged.
//   - If the existing operation is terminal (or none exists), a new,
//     distinct operation with a new monotonic ID is started (terminal retry).
//
// Exception — a protected failure is not freely retryable. If the existing
// operation failed during activation (installing/restarting/healthcheck), it
// records RecoveryNeeded: the installation may be partially activated, so
// Start is refused (recovery-needed) until the backend confirms a safe
// failed state (backend-failed) or a successful rollback
// (backend-recovered, or the explicit recover). There is no implicit
// recovered.
func (s *Store) Start(installationID, targetVersion string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if installationID == "" || targetVersion == "" {
		return Snapshot{}, failureErr(MachineInvalidRequest,
			"an operation needs an installation identity and a target version")
	}

	if cur, ok := s.ops[installationID]; ok {
		if !cur.State.Terminal() {
			if cur.TargetVersion == targetVersion {
				return *cur, nil
			}
			return Snapshot{}, failureErr(MachineConflictingTarget,
				"an update to "+cur.TargetVersion+" is already in progress for this installation")
		}
		if cur.State == StateFailed && cur.RecoveryNeeded {
			return Snapshot{}, failureErr(MachineRecoveryNeeded,
				"the previous update failed during activation; a new update can only start after the backend confirms it is safe")
		}
	}

	id, err := s.allocateID()
	if err != nil {
		return Snapshot{}, err
	}
	now := time.Now()
	op := &Snapshot{
		ID:             id,
		InstallationID: installationID,
		TargetVersion:  targetVersion,
		State:          StateChecking,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	s.ops[installationID] = op
	return *op, nil
}

// Snapshot returns an immutable copy of the installation's current operation,
// or ok=false when none exists.
func (s *Store) Snapshot(installationID string) (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.ops[installationID]
	if !ok {
		return Snapshot{}, false
	}
	return *op, true
}

// Apply applies an explicit event to the installation's current operation
// and returns the updated immutable snapshot.
//
// Input.OperationID must name the installation's current operation: a zero
// ID (operation-id-required) and any non-current ID (stale-operation) are
// refused and leave the model unchanged; there is no default-to-current
// path.
//
// Unlisted edges, guard violations, and malformed inputs are refused with an
// *Error carrying a machine code and a user reason; the operation is then
// unchanged. As a deliberate exception, EventCancel inside the protected
// region (installing/restarting/healthcheck) is not an error and not a
// cancellation: it records RecoveryRequested, leaves the state unchanged,
// and freezes all ordinary advancement until the backend's safe callback
// (EventBackendRecovered or EventBackendFailed) resolves the state.
func (s *Store) Apply(installationID string, in Input) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	op, ok := s.ops[installationID]
	if !ok {
		return Snapshot{}, failureErr(MachineMissingOperation,
			"no update operation exists for this installation")
	}

	// Explicit operation keying. Every mutation must name the operation it
	// targets: a zero ID and any ID other than the current operation's are
	// refused and leave the model unchanged. There is deliberately no
	// default-to-current path, so a stale event from a previous (for
	// example cancelled) operation can never advance the new one.
	if in.OperationID == 0 {
		return Snapshot{}, failureErr(MachineOperationIDRequired,
			"every update step must name the operation it applies to")
	}
	if in.OperationID != op.ID {
		return Snapshot{}, failureErr(MachineStaleOperation,
			"that step belongs to a previous update operation and cannot be applied to the current one")
	}

	// A cancel inside the protected region can never be honored as a
	// cancellation. Mark the recovery request and wait for the backend's
	// safe callback; the state does not change. Once the request is
	// recorded, a repeat cancel is just another frozen event.
	if in.Event == EventCancel && protectedFromCancel[op.State] {
		if op.RecoveryRequested {
			return Snapshot{}, failureErr(MachineRecoveryPending,
				"the cancellation is already recorded; the update is frozen until the backend reports a safe outcome")
		}
		op.RecoveryRequested = true
		op.UpdatedAt = time.Now()
		return *op, nil
	}

	// Freeze: while a protected-region cancellation is pending, no
	// ordinary event — not even the normal path to complete — may advance
	// the operation. Only the backend's explicit safe outcome resolves it.
	if op.RecoveryRequested && in.Event != EventBackendRecovered && in.Event != EventBackendFailed {
		return Snapshot{}, failureErr(MachineRecoveryPending,
			"a cancellation was requested during activation; the update is frozen until the backend reports a safe outcome")
	}

	// Backend safe callbacks are only credible after a recorded recovery
	// request, or (for a protected failure) after the failed operation
	// recorded that the backend must confirm a safe failed state or a
	// successful rollback; without one they cannot conjure recovery or
	// failure resolution out of nowhere.
	if (in.Event == EventBackendRecovered || in.Event == EventBackendFailed) &&
		!op.RecoveryRequested && !(op.State == StateFailed && op.RecoveryNeeded) {
		return Snapshot{}, refusal(op.State, in.Event)
	}

	next, legal := transitions[op.State][in.Event]
	if !legal {
		return Snapshot{}, refusal(op.State, in.Event)
	}

	// Event-specific validation.
	switch in.Event {
	case EventFail:
		if in.Failure.Code == "" || in.Failure.UserReason == "" {
			return Snapshot{}, failureErr(MachineInvalidRequest,
				"a failure needs a machine code and a user-facing reason")
		}
	case EventDrain:
		// Wait for idle: waiting must not auto-activate while active work
		// exists.
		if op.ActiveWork {
			return Snapshot{}, failureErr(MachineActiveWorkPresent,
				"active work is still running; the update will start once it finishes or you cancel it explicitly")
		}
	case EventDrained:
		// Draining is not finished while the consent-cancelled work is
		// still running: only the explicit idle callback clears it.
		if op.ActiveWork {
			return Snapshot{}, failureErr(MachineActiveWorkPresent,
				"the cancelled work has not finished yet; draining completes once it reports idle")
		}
	case EventCancelActiveWork:
		// Cancelling active work requires explicit user consent, and only
		// applies when work is actually active.
		if !in.Consent {
			return Snapshot{}, failureErr(MachineConsentRequired,
				"cancelling active work needs your explicit confirmation")
		}
		if !op.ActiveWork {
			return Snapshot{}, failureErr(MachineInvalidRequest,
				"there is no active work to cancel")
		}
	}

	from := op.State
	op.State = next
	op.UpdatedAt = time.Now()

	switch in.Event {
	case EventWorkActive:
		op.ActiveWork = true
	case EventIdle:
		// The only path that clears active work, in waiting and in
		// draining alike.
		op.ActiveWork = false
	case EventCancelActiveWork:
		// Consent was validated above and draining begins, but the work
		// itself stays active: it is only cleared by the explicit idle
		// callback, and drained refuses until then.
		op.ConsentRecorded = true
	case EventCancel, EventDefer:
		// Pre-activation cancel/defer restores normal admission. The real
		// admission mechanism arrives in a later slice; this is the
		// model-level flag.
		op.ResumeAdmission = true
	case EventResume:
		// Resuming restarts the update lifecycle; the previous
		// defer's admission flag is no longer current.
		op.ResumeAdmission = false
	case EventHealthSuccess:
		// Health verification passed; only now may old-version cleanup
		// begin.
		op.HealthVerified = true
	case EventHealthFail:
		// A health failure always carries a safe, fixed machine code and
		// a sanitized reason composed here, never from caller input.
		op.Failure = Failure{Code: MachineHealthCheckFailed, UserReason: healthFailUserReason}
		op.HasFailure = true
		// The failure happened during activation: recovery is blocked
		// until the backend confirms a safe outcome.
		op.RecoveryNeeded = true
	case EventCleanupPending:
		// Explicit success with pending cleanup, never a silent success.
		op.SuccessWithPendingCleanup = true
	case EventFail:
		op.Failure = in.Failure
		op.HasFailure = true
		// A failure during the protected region means the installation
		// may be partially activated: the failed operation blocks a new
		// Start until the backend confirms a safe failed state or a
		// successful rollback. A failure before activation is an ordinary
		// terminal failure.
		if protectedFromCancel[from] {
			op.RecoveryNeeded = true
		}
	case EventBackendRecovered, EventBackendFailed:
		// The backend's safe outcome resolves the pending request and, if
		// any, the recovery block.
		op.RecoveryRequested = false
		op.RecoveryNeeded = false
	case EventRecover:
		// Explicit recovery restores the previous version; it is a
		// distinct terminal outcome, never completion, and it lifts the
		// recovery block.
		op.RecoveryNeeded = false
	}

	return *op, nil
}

// refusal builds the standard illegal-transition error.
func refusal(from State, ev Event) *Error {
	return failureErr(MachineIllegalTransition,
		string(ev)+" is not a legal step from the "+string(from)+" state")
}
