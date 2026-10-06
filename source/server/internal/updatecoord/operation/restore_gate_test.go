package operation

import (
	"errors"
	"testing"
)

// The restore gate is the boundary between a durable operation record and
// the pure model: RestoreSnapshot must accept every snapshot the model can
// actually produce through its real transition table and refuse every
// record the model provably cannot have produced. These probes pin BOTH
// directions against actual Apply — never against the gate itself.

// probeFailure is the fixture failure used by the reachable-state
// exploration: a valid machine code plus a fixed user reason.
var probeFailure = Failure{Code: MachineDownloadFailed, UserReason: "fixture failure for the reachable-state probe"}

// allProbeEvents is every event the model defines, in a fixed order so the
// reachable-state exploration below is fully deterministic.
var allProbeEvents = []Event{
	EventAnnounce, EventReady, EventDownload, EventDownloaded,
	EventVerified, EventWorkActive, EventIdle, EventDrain,
	EventCancelActiveWork, EventDrained, EventInstalled, EventRestarted,
	EventHealthSuccess, EventHealthFail, EventCleanupDone,
	EventCleanupPending, EventCancel, EventDefer, EventResume,
	EventFail, EventRecover, EventBackendRecovered, EventBackendFailed,
}

// allProbeStates is every observable lifecycle state.
var allProbeStates = []State{
	StateChecking, StateAnnounced, StateReady, StateDownloading,
	StateVerifying, StateWaiting, StateDraining, StateInstalling,
	StateRestarting, StateHealthCheck, StateCleanup, StateComplete,
	StateDeferred, StateCancelled, StateFailed, StateRecovered,
}

// snapshotKey is a Snapshot minus its clock and identifier: the dedup key
// for the reachable-state exploration. Two snapshots with the same key are
// the same persisted-record content up to bookkeeping.
type snapshotKey struct {
	State                     State
	ActiveWork                bool
	ConsentRecorded           bool
	RecoveryRequested         bool
	RecoveryNeeded            bool
	ResumeAdmission           bool
	HealthVerified            bool
	SuccessWithPendingCleanup bool
	HasFailure                bool
	Failure                   Failure
}

func keyOf(s Snapshot) snapshotKey {
	return snapshotKey{
		State:                     s.State,
		ActiveWork:                s.ActiveWork,
		ConsentRecorded:           s.ConsentRecorded,
		RecoveryRequested:         s.RecoveryRequested,
		RecoveryNeeded:            s.RecoveryNeeded,
		ResumeAdmission:           s.ResumeAdmission,
		HealthVerified:            s.HealthVerified,
		SuccessWithPendingCleanup: s.SuccessWithPendingCleanup,
		HasFailure:                s.HasFailure,
		Failure:                   s.Failure,
	}
}

// cloneReachable rebuilds the model at the end of a path of legal events
// from a fresh operation. This is the clone mechanism for reachable
// snapshots: it uses only Start and Apply — never Store.Restore and never
// RestoreSnapshot — so the probes below cannot be fooled by the very code
// they are testing.
func cloneReachable(t *testing.T, path []Input) Snapshot {
	t.Helper()
	s := NewStore()
	mustStart(t, s, installA, targetV)
	for i, in := range path {
		in.OperationID = currentID(t, s, installA)
		if _, err := s.Apply(installA, in); err != nil {
			t.Fatalf("probe path step %d (%s) is not legal: %v", i, in.Event, err)
		}
	}
	snap, _ := s.Snapshot(installA)
	return snap
}

// TestRestorePropertyEveryReachableSnapshotRestores is the deterministic,
// bounded reachable-state property test for the restore gate. It explores
// the model's actual event space by exhaustive breadth-first search over
// real Apply, deduplicating snapshots by everything except clock and ID,
// and asserting that EVERY actually reachable snapshot:
//
//   - is accepted by RestoreSnapshot through its Record projection, and
//   - restores with every field preserved exactly.
//
// Because the exploration uses the real transition table (with consent
// true/false, the fixture failure, and the real current operation ID on
// every attempt), any whitelist in the gate that rejects a snapshot the
// model can actually produce fails here.
func TestRestorePropertyEveryReachableSnapshotRestores(t *testing.T) {
	type node struct {
		key  snapshotKey
		path []Input
	}
	seen := map[snapshotKey]bool{}
	statesCovered := map[State]bool{}
	seed := cloneReachable(t, nil) // the fresh operation is the seed
	queue := []node{{key: keyOf(seed), path: nil}}
	var applies, accepted, restored int

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur.key] {
			continue
		}
		seen[cur.key] = true

		snap := cloneReachable(t, cur.path)
		if keyOf(snap) != cur.key {
			t.Fatalf("probe replay diverged from its dedup key:\n got %+v\nwant %+v", keyOf(snap), cur.key)
		}
		statesCovered[snap.State] = true

		// THE PROPERTY: every snapshot the model actually produces must
		// pass the persisted-record restore gate unchanged.
		got, err := RestoreSnapshot(snap.Record())
		if err != nil {
			t.Fatalf("restore refused an actually reachable snapshot %+v: %v", keyOf(snap), err)
		}
		if got != snap {
			t.Fatalf("restore did not preserve every field:\n got %+v\nwant %+v", got, snap)
		}
		restored++

		// Explore onward: exhaust every event with consent true/false and
		// the fixture failure, each against a fresh clone of this exact
		// snapshot, naming the real current operation ID every time.
		for _, ev := range allProbeEvents {
			for _, consent := range []bool{false, true} {
				in := Input{Event: ev, Consent: consent}
				if ev == EventFail {
					in.Failure = probeFailure
				}
				s := NewStore()
				mustStart(t, s, installA, targetV)
				for _, step := range cur.path {
					step.OperationID = currentID(t, s, installA)
					if _, err := s.Apply(installA, step); err != nil {
						t.Fatalf("probe path step (%s) is not legal: %v", step.Event, err)
					}
				}
				in.OperationID = currentID(t, s, installA)
				applies++
				next, err := s.Apply(installA, in)
				if err != nil {
					continue // refused edges contribute nothing reachable
				}
				accepted++
				k := keyOf(next)
				if !seen[k] {
					queue = append(queue, node{
						key:  k,
						path: append(append([]Input{}, cur.path...), in),
					})
				}
			}
		}
	}

	// Coverage guards: the exploration must actually cover the model.
	for _, st := range allProbeStates {
		if !statesCovered[st] {
			t.Errorf("reachable-state exploration never reached state %q", st)
		}
	}
	if len(seen) < 25 {
		t.Errorf("reachable-state exploration found only %d distinct snapshots; the flag space is under-explored", len(seen))
	}
	t.Logf("restore gate property: %d distinct reachable snapshots restored, %d/%d states covered, %d event-variant applies, %d accepted",
		restored, len(statesCovered), len(allProbeStates), applies, accepted)
}

// reachable drives a list of legal inputs from a fresh operation and
// returns the resulting snapshot, failing the test on any refusal.
func reachable(t *testing.T, steps ...Input) Snapshot {
	t.Helper()
	s := NewStore()
	mustStart(t, s, installA, targetV)
	var snap Snapshot
	for _, in := range steps {
		snap = mustApply(t, s, installA, in)
	}
	return snap
}

func expectRestores(t *testing.T, snap Snapshot) {
	t.Helper()
	got, err := RestoreSnapshot(snap.Record())
	if err != nil {
		t.Fatalf("restore refused an actually reachable snapshot (state=%s, active=%v, consent=%v, health=%v, failure=%v): %v",
			snap.State, snap.ActiveWork, snap.ConsentRecorded, snap.HealthVerified, snap.HasFailure, err)
	}
	if got != snap {
		t.Fatalf("restore did not preserve every field:\n got %+v\nwant %+v", got, snap)
	}
}

// expectRefused asserts the record fails the gate with MachineInvalidRecord.
func expectRefused(t *testing.T, rec Record) {
	t.Helper()
	_, err := RestoreSnapshot(rec)
	if err == nil {
		t.Fatalf("restore accepted a record the model cannot produce: %+v", rec)
	}
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != MachineInvalidRecord {
		t.Fatalf("refusal = %v, want invalid-record", err)
	}
}

// PROBE 1 (fails before the fix): cleanup and complete are reachable ONLY
// through health success, so a record claiming either state WITHOUT
// HealthVerified cannot have been produced by the model and must be
// refused. The gate currently accepts them: the converse direction of the
// health invariant is missing.
func TestRestoreRefusesCleanupOrCompleteWithoutVerifiedHealth(t *testing.T) {
	// Genuinely reachable control records (with health) restore.
	cleanupSnap := reachable(t,
		Input{Event: EventReady}, Input{Event: EventDownload},
		Input{Event: EventDownloaded}, Input{Event: EventVerified},
		Input{Event: EventDrain}, Input{Event: EventDrained},
		Input{Event: EventInstalled}, Input{Event: EventRestarted},
		Input{Event: EventHealthSuccess},
	)
	if cleanupSnap.State != StateCleanup || !cleanupSnap.HealthVerified {
		t.Fatalf("setup: cleanup probe base = %+v", cleanupSnap)
	}
	expectRestores(t, cleanupSnap)

	completeSnap := reachable(t,
		Input{Event: EventReady}, Input{Event: EventDownload},
		Input{Event: EventDownloaded}, Input{Event: EventVerified},
		Input{Event: EventDrain}, Input{Event: EventDrained},
		Input{Event: EventInstalled}, Input{Event: EventRestarted},
		Input{Event: EventHealthSuccess}, Input{Event: EventCleanupDone},
	)
	if completeSnap.State != StateComplete || !completeSnap.HealthVerified {
		t.Fatalf("setup: complete probe base = %+v", completeSnap)
	}
	expectRestores(t, completeSnap)

	// The impossible converse: the same records with the health flag
	// stripped. Cleanup/complete are reachable only through health success,
	// so these can never have been produced by the model.
	rec := cleanupSnap.Record()
	rec.HealthVerified = false
	expectRefused(t, rec)

	rec = completeSnap.Record()
	rec.HealthVerified = false
	expectRefused(t, rec)

	// Success-with-pending-cleanup completion equally requires health.
	pendingSnap := reachable(t,
		Input{Event: EventReady}, Input{Event: EventDownload},
		Input{Event: EventDownloaded}, Input{Event: EventVerified},
		Input{Event: EventDrain}, Input{Event: EventDrained},
		Input{Event: EventInstalled}, Input{Event: EventRestarted},
		Input{Event: EventHealthSuccess}, Input{Event: EventCleanupPending},
	)
	rec = pendingSnap.Record()
	rec.HealthVerified = false
	expectRefused(t, rec)
}

// PROBE 2 (fails before the fix): health verification survives a cleanup
// failure (cleanup -> failed) and the explicit recover into recovered. The
// recovered-with-health snapshot is actually reachable and must restore; the
// gate's per-state health whitelist currently rejects it.
func TestRestoreAcceptsHealthSurvivingCleanupFailureToRecovered(t *testing.T) {
	snap := reachable(t,
		Input{Event: EventReady}, Input{Event: EventDownload},
		Input{Event: EventDownloaded}, Input{Event: EventVerified},
		Input{Event: EventDrain}, Input{Event: EventDrained},
		Input{Event: EventInstalled}, Input{Event: EventRestarted},
		Input{Event: EventHealthSuccess},
		Input{Event: EventFail, Failure: probeFailure},
		Input{Event: EventRecover},
	)
	if snap.State != StateRecovered || !snap.HealthVerified || !snap.HasFailure {
		t.Fatalf("setup: recovered-after-cleanup-failure probe base = %+v", snap)
	}
	if snap.RecoveryNeeded {
		t.Fatalf("setup: a cleanup failure is not a protected failure: %+v", snap)
	}
	expectRestores(t, snap)
}

// PROBE 3 (fails before the fix): active work is cleared only by EventIdle,
// so it survives defer into deferred AND resume back into the
// pre-activation states (ready, downloading, verifying) — and a subsequent
// failure plus explicit recover carries it into recovered. All of these are
// actually reachable and must restore; the gate's active-work whitelist
// currently rejects them.
func TestRestoreAcceptsActiveWorkSurvivingDeferResume(t *testing.T) {
	base := []Input{
		Input{Event: EventReady}, Input{Event: EventDownload},
		Input{Event: EventDownloaded}, Input{Event: EventVerified},
		Input{Event: EventWorkActive},
	}
	// waiting with active work, then defer: reachable (the gate accepts it
	// today — the whitelist includes deferred).
	deferred := reachable(t, append(append([]Input{}, base...), Input{Event: EventDefer})...)
	if deferred.State != StateDeferred || !deferred.ActiveWork {
		t.Fatalf("setup: deferred-with-active-work = %+v", deferred)
	}
	expectRestores(t, deferred)

	// resume: the work never reported idle, so it is STILL active in ready.
	resumed := reachable(t, append(append([]Input{}, base...), Input{Event: EventDefer}, Input{Event: EventResume})...)
	if resumed.State != StateReady || !resumed.ActiveWork {
		t.Fatalf("setup: resumed-with-active-work = %+v", resumed)
	}
	expectRestores(t, resumed)

	downloading := reachable(t, append(append([]Input{}, base...),
		Input{Event: EventDefer}, Input{Event: EventResume}, Input{Event: EventDownload})...)
	if downloading.State != StateDownloading || !downloading.ActiveWork {
		t.Fatalf("setup: downloading-with-active-work = %+v", downloading)
	}
	expectRestores(t, downloading)

	verifying := reachable(t, append(append([]Input{}, base...),
		Input{Event: EventDefer}, Input{Event: EventResume},
		Input{Event: EventDownload}, Input{Event: EventDownloaded})...)
	if verifying.State != StateVerifying || !verifying.ActiveWork {
		t.Fatalf("setup: verifying-with-active-work = %+v", verifying)
	}
	expectRestores(t, verifying)

	// The carried work also survives a pre-activation failure and the
	// explicit recover into recovered.
	recovered := reachable(t, append(append([]Input{}, base...),
		Input{Event: EventDefer}, Input{Event: EventResume},
		Input{Event: EventFail, Failure: probeFailure}, Input{Event: EventRecover})...)
	if recovered.State != StateRecovered || !recovered.ActiveWork {
		t.Fatalf("setup: recovered-with-active-work = %+v", recovered)
	}
	expectRestores(t, recovered)
}

// Invariant refusal probes: records carrying recovery flags the model can
// provably never record in the claimed state must be refused by the gate —
// never silently reset or coerced.
func TestRestoreRefusesInconsistentRecoveryFlags(t *testing.T) {
	// A pending recovery request exists only inside the protected region:
	// a cancel anywhere else is honored as a cancellation immediately.
	cancelled := reachable(t, Input{Event: EventCancel})
	if cancelled.State != StateCancelled || cancelled.RecoveryRequested {
		t.Fatalf("setup: cancelled base = %+v", cancelled)
	}
	for _, rec := range []Record{
		func() Record { r := cancelled.Record(); r.RecoveryRequested = true; return r }(),
		func() Record { r := cancelled.Record(); r.RecoveryRequested = true; r.RecoveryNeeded = true; return r }(),
	} {
		expectRefused(t, rec)
	}

	// A blocked recovery exists only on a failed operation; every other
	// recorded recovery flag combination is impossible.
	for _, base := range []Snapshot{
		cancelled,
		reachable(t,
			Input{Event: EventReady}, Input{Event: EventDownload},
			Input{Event: EventDownloaded}, Input{Event: EventVerified},
			Input{Event: EventDrain}, Input{Event: EventDrained},
			Input{Event: EventInstalled}, Input{Event: EventRestarted},
			Input{Event: EventHealthSuccess}, Input{Event: EventCleanupDone},
		),
	} {
		rec := base.Record()
		rec.RecoveryNeeded = true
		expectRefused(t, rec)
	}

	// A failed operation can hold RecoveryNeeded, but never a pending
	// recovery request: the freeze is resolved by the backend callback
	// before the state may change.
	failed := reachable(t,
		Input{Event: EventReady}, Input{Event: EventDownload},
		Input{Event: EventDownloaded}, Input{Event: EventVerified},
		Input{Event: EventDrain}, Input{Event: EventDrained},
		Input{Event: EventFail, Failure: probeFailure},
	)
	if failed.State != StateFailed || !failed.RecoveryNeeded {
		t.Fatalf("setup: protected-failure base = %+v", failed)
	}
	expectRestores(t, failed) // the consistent combination restores
	rec := failed.Record()
	rec.RecoveryRequested = true
	expectRefused(t, rec)
}
