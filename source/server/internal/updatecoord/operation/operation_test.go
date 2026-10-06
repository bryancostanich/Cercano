package operation

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

// These tests are pure: they exercise the in-memory state model only. They
// never touch the filesystem, never start processes, and never reach the
// network.

const (
	installA = "install-a"
	installB = "install-b"
	targetV  = "0.9.0"
)

func mustStart(t *testing.T, s *Store, installation, target string) Snapshot {
	t.Helper()
	snap, err := s.Start(installation, target)
	if err != nil {
		t.Fatalf("Start(%q, %q) failed: %v", installation, target, err)
	}
	return snap
}

// currentID returns the installation's current operation ID. Every mutation
// must name it explicitly; helpers attach it so refusals under test are due
// to the rule under test, never a missing ID.
func currentID(t *testing.T, s *Store, installation string) int64 {
	t.Helper()
	snap, ok := s.Snapshot(installation)
	if !ok {
		t.Fatalf("no operation exists for %q", installation)
	}
	return snap.ID
}

func mustApply(t *testing.T, s *Store, installation string, in Input) Snapshot {
	t.Helper()
	in.OperationID = currentID(t, s, installation)
	snap, err := s.Apply(installation, in)
	if err != nil {
		t.Fatalf("Apply(%q, %s) failed: %v", installation, in.Event, err)
	}
	return snap
}

func applyErr(t *testing.T, s *Store, installation string, in Input) *Error {
	t.Helper()
	if snap, ok := s.Snapshot(installation); ok {
		in.OperationID = snap.ID
	}
	_, err := s.Apply(installation, in)
	if err == nil {
		t.Fatalf("Apply(%q, %s) unexpectedly succeeded", installation, in.Event)
	}
	var opErr *Error
	if !errors.As(err, &opErr) {
		t.Fatalf("Apply error is %T, want *operation.Error: %v", err, err)
	}
	if opErr.Code == "" || opErr.UserReason == "" {
		t.Fatalf("refusal missing machine code or user reason: %+v", opErr)
	}
	return opErr
}

func failInput(code MachineCode, reason string) Input {
	return Input{Event: EventFail, Failure: Failure{Code: code, UserReason: reason}}
}

// driveToWaiting moves a fresh operation through the pre-activation stages.
func driveToWaiting(t *testing.T, s *Store, installation, target string) Snapshot {
	t.Helper()
	mustStart(t, s, installation, target)
	mustApply(t, s, installation, Input{Event: EventReady})
	mustApply(t, s, installation, Input{Event: EventDownload})
	mustApply(t, s, installation, Input{Event: EventDownloaded})
	return mustApply(t, s, installation, Input{Event: EventVerified})
}

// driveToInstalling additionally drains and activates.
func driveToInstalling(t *testing.T, s *Store, installation, target string) Snapshot {
	t.Helper()
	driveToWaiting(t, s, installation, target)
	mustApply(t, s, installation, Input{Event: EventDrain})
	return mustApply(t, s, installation, Input{Event: EventDrained})
}

func TestHappyPath(t *testing.T) {
	s := NewStore()
	snap := mustStart(t, s, installA, targetV)
	if snap.State != StateChecking || snap.ID != 1 {
		t.Fatalf("initial = %+v", snap)
	}
	for _, ev := range []Event{
		EventReady, EventDownload, EventDownloaded, EventVerified,
		EventDrain, EventDrained, EventInstalled, EventRestarted,
		EventHealthSuccess, EventCleanupDone,
	} {
		snap = mustApply(t, s, installA, Input{Event: ev})
	}
	if snap.State != StateComplete {
		t.Fatalf("state = %q, want complete", snap.State)
	}
	if !snap.HealthVerified {
		t.Fatal("complete without health verification")
	}
	if snap.SuccessWithPendingCleanup {
		t.Fatal("ordinary completion must not claim pending cleanup")
	}
	if !snap.State.Terminal() {
		t.Fatal("complete must be terminal")
	}
}

func TestWaitForIdleDoesNotAutoActivate(t *testing.T) {
	s := NewStore()
	driveToWaiting(t, s, installA, targetV)

	// Active work holds the operation in waiting.
	snap := mustApply(t, s, installA, Input{Event: EventWorkActive})
	if snap.State != StateWaiting || !snap.ActiveWork {
		t.Fatalf("after work-active: %+v", snap)
	}

	// A drain/activation request must be refused while work is active:
	// waiting never auto-activates.
	applyErr(t, s, installA, Input{Event: EventDrain})
	if snap, _ := s.Snapshot(installA); snap.State != StateWaiting {
		t.Fatalf("state after refused drain = %q, want waiting", snap.State)
	}

	// Only real idleness admits activation.
	mustApply(t, s, installA, Input{Event: EventIdle})
	snap = mustApply(t, s, installA, Input{Event: EventDrain})
	if snap.State != StateDraining {
		t.Fatalf("state after idle+drain = %q, want draining", snap.State)
	}
}

func TestCancelActiveWorkRequiresConsent(t *testing.T) {
	s := NewStore()
	driveToWaiting(t, s, installA, targetV)
	mustApply(t, s, installA, Input{Event: EventWorkActive})

	// Without consent: refused, work still active, still waiting.
	err := applyErr(t, s, installA, Input{Event: EventCancelActiveWork})
	if err.Code != MachineConsentRequired {
		t.Fatalf("code = %q, want consent-required", err.Code)
	}
	if snap, _ := s.Snapshot(installA); snap.State != StateWaiting || !snap.ActiveWork || snap.ConsentRecorded {
		t.Fatalf("refused cancel-active-work changed the model: %+v", snap)
	}

	// With explicit consent: draining begins and consent is recorded, but
	// the work itself stays active until its own idle callback.
	snap := mustApply(t, s, installA, Input{Event: EventCancelActiveWork, Consent: true})
	if snap.State != StateDraining || !snap.ConsentRecorded {
		t.Fatalf("consented cancel-active-work: %+v", snap)
	}
	if !snap.ActiveWork {
		t.Fatal("consent must not silently clear active work; only idle may")
	}

	// Drained refuses while the cancelled work is still running.
	applyErr(t, s, installA, Input{Event: EventDrained})

	// The explicit idle callback finishes the cancellation.
	snap = mustApply(t, s, installA, Input{Event: EventIdle})
	if snap.State != StateDraining || snap.ActiveWork {
		t.Fatalf("idle after consent: %+v", snap)
	}
	snap = mustApply(t, s, installA, Input{Event: EventDrained})
	if snap.State != StateInstalling {
		t.Fatalf("drained after idle: state = %q, want installing", snap.State)
	}
}

func TestNoForcedWorkCancel(t *testing.T) {
	// With active work present, there must be no event path that clears it
	// or leaves waiting except: consented cancel-active-work, real idle,
	// legal cancel/defer/fail of the operation itself. Unattended
	// deadline-based forced cancellation has no event in the model at all.
	s := NewStore()
	driveToWaiting(t, s, installA, targetV)
	mustApply(t, s, installA, Input{Event: EventWorkActive})

	for _, ev := range []Event{
		EventDownload, EventDownloaded, EventVerified, EventDrained,
		EventInstalled, EventRestarted, EventHealthSuccess, EventHealthFail,
		EventCleanupDone, EventCleanupPending, EventResume, EventRecover,
		EventBackendRecovered, EventBackendFailed, EventAnnounce,
	} {
		before, _ := s.Snapshot(installA)
		applyErr(t, s, installA, Input{Event: ev})
		after, _ := s.Snapshot(installA)
		if before.State != after.State || before.ActiveWork != after.ActiveWork {
			t.Fatalf("event %s mutated waiting: %+v -> %+v", ev, before, after)
		}
	}
}

func TestCancelBeforeActivationLegal(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, s *Store)
	}{
		{"checking", func(t *testing.T, s *Store) { mustStart(t, s, installA, targetV) }},
		{"announced", func(t *testing.T, s *Store) {
			mustStart(t, s, installA, targetV)
			mustApply(t, s, installA, Input{Event: EventAnnounce})
		}},
		{"ready", func(t *testing.T, s *Store) {
			mustStart(t, s, installA, targetV)
			mustApply(t, s, installA, Input{Event: EventReady})
		}},
		{"downloading", func(t *testing.T, s *Store) {
			mustStart(t, s, installA, targetV)
			mustApply(t, s, installA, Input{Event: EventReady})
			mustApply(t, s, installA, Input{Event: EventDownload})
		}},
		{"verifying", func(t *testing.T, s *Store) {
			mustStart(t, s, installA, targetV)
			mustApply(t, s, installA, Input{Event: EventReady})
			mustApply(t, s, installA, Input{Event: EventDownload})
			mustApply(t, s, installA, Input{Event: EventDownloaded})
		}},
		{"waiting", func(t *testing.T, s *Store) { driveToWaiting(t, s, installA, targetV) }},
		{"draining", func(t *testing.T, s *Store) {
			driveToWaiting(t, s, installA, targetV)
			mustApply(t, s, installA, Input{Event: EventDrain})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			tc.prepare(t, s)
			snap := mustApply(t, s, installA, Input{Event: EventCancel})
			if snap.State != StateCancelled {
				t.Fatalf("state = %q, want cancelled", snap.State)
			}
			if !snap.ResumeAdmission {
				t.Fatal("pre-activation cancel must model restored admission")
			}
		})
	}
}

func TestCancelDuringProtectedRegionCannotFalselyCancel(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, s *Store)
	}{
		{"installing", func(t *testing.T, s *Store) {
			driveToInstalling(t, s, installA, targetV)
		}},
		{"restarting", func(t *testing.T, s *Store) {
			driveToInstalling(t, s, installA, targetV)
			mustApply(t, s, installA, Input{Event: EventInstalled})
		}},
		{"healthcheck", func(t *testing.T, s *Store) {
			driveToInstalling(t, s, installA, targetV)
			mustApply(t, s, installA, Input{Event: EventInstalled})
			mustApply(t, s, installA, Input{Event: EventRestarted})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			tc.prepare(t, s)
			before, _ := s.Snapshot(installA)

			// The cancel attempt is not an error and not a cancellation:
			// the state stays put and only RecoveryRequested is recorded.
			snap, err := s.Apply(installA, Input{Event: EventCancel, OperationID: before.ID})
			if err != nil {
				t.Fatalf("cancel in %s failed: %v", tc.name, err)
			}
			if snap.State != before.State {
				t.Fatalf("state = %q, want unchanged %q (no false cancellation)", snap.State, before.State)
			}
			if !snap.RecoveryRequested {
				t.Fatalf("cancel in %s must mark recovery-requested", tc.name)
			}

			// Backend safe callbacks are refused without the recorded
			// recovery request.
			s2 := NewStore()
			tc.prepare(t, s2)
			applyErr(t, s2, installA, Input{Event: EventBackendRecovered})
			applyErr(t, s2, installA, Input{Event: EventBackendFailed})

			// Recovered path: safe callback resolves to recovered.
			snap = mustApply(t, s, installA, Input{Event: EventBackendRecovered})
			if snap.State != StateRecovered || !snap.State.Terminal() {
				t.Fatalf("after backend-recovered: %+v", snap)
			}

			// Failed path: safe callback resolves to failed, then explicit
			// recover — recovered, never complete.
			s3 := NewStore()
			tc.prepare(t, s3)
			mustApply(t, s3, installA, Input{Event: EventCancel}) // marks recovery requested
			snap = mustApply(t, s3, installA, Input{Event: EventBackendFailed})
			if snap.State != StateFailed {
				t.Fatalf("after backend-failed: state=%q, want failed", snap.State)
			}
			snap = mustApply(t, s3, installA, Input{Event: EventRecover})
			if snap.State != StateRecovered {
				t.Fatalf("explicit recover: state=%q, want recovered", snap.State)
			}
		})
	}
}

func TestCompleteRequiresHealthAndCleanup(t *testing.T) {
	// Health success and cleanup done is the ordinary completion.
	s := NewStore()
	driveToInstalling(t, s, installA, targetV)
	mustApply(t, s, installA, Input{Event: EventInstalled})
	mustApply(t, s, installA, Input{Event: EventRestarted})
	mustApply(t, s, installA, Input{Event: EventHealthSuccess})
	snap := mustApply(t, s, installA, Input{Event: EventCleanupDone})
	if snap.State != StateComplete || snap.SuccessWithPendingCleanup {
		t.Fatalf("ordinary completion: %+v", snap)
	}

	// Explicit success with pending cleanup (for example locked files):
	// completion is granted only through the explicit pending flag, never
	// silently.
	s2 := NewStore()
	driveToInstalling(t, s2, installA, targetV)
	mustApply(t, s2, installA, Input{Event: EventInstalled})
	mustApply(t, s2, installA, Input{Event: EventRestarted})
	mustApply(t, s2, installA, Input{Event: EventHealthSuccess})
	snap = mustApply(t, s2, installA, Input{Event: EventCleanupPending})
	if snap.State != StateComplete || !snap.SuccessWithPendingCleanup {
		t.Fatalf("pending-cleanup completion: %+v", snap)
	}
}

func TestOldCleanupHealthInvariant(t *testing.T) {
	// The superseded (old) version may only be cleaned up after health
	// verification succeeded. Structurally: the only edge into cleanup is
	// health-success from healthcheck, and the only edges into complete
	// come from cleanup. Behaviorally: health/cleanup events are refused
	// from every other state and never mutate the model.
	states := []State{
		StateChecking, StateAnnounced, StateReady, StateDownloading,
		StateVerifying, StateWaiting, StateDraining, StateInstalling,
		StateRestarting, StateHealthCheck, StateCleanup, StateComplete,
		StateDeferred, StateCancelled, StateFailed, StateRecovered,
	}
	for _, from := range states {
		for _, ev := range []Event{EventHealthSuccess, EventCleanupDone, EventCleanupPending} {
			if ev == EventHealthSuccess && from != StateHealthCheck {
				if _, listed := transitions[from][ev]; listed {
					t.Fatalf("health-success is a legal edge from %q", from)
				}
			}
			if ev != EventHealthSuccess && from != StateCleanup {
				if _, listed := transitions[from][ev]; listed {
					t.Fatalf("%s is a legal edge from %q", ev, from)
				}
			}
		}
		for ev, to := range transitions[from] {
			if to == StateComplete && from != StateCleanup {
				t.Fatalf("edge %s: %q -> complete bypasses cleanup", ev, from)
			}
			if to == StateCleanup && from != StateHealthCheck {
				t.Fatalf("edge %s: %q -> cleanup bypasses health verification", ev, from)
			}
		}
	}

	// Behaviorally, from a sample of pre-cleanup states.
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, s *Store)
	}{
		{"checking", func(t *testing.T, s *Store) { mustStart(t, s, installA, targetV) }},
		{"ready", func(t *testing.T, s *Store) {
			mustStart(t, s, installA, targetV)
			mustApply(t, s, installA, Input{Event: EventReady})
		}},
		{"waiting", func(t *testing.T, s *Store) { driveToWaiting(t, s, installA, targetV) }},
		{"restarting", func(t *testing.T, s *Store) {
			driveToInstalling(t, s, installA, targetV)
			mustApply(t, s, installA, Input{Event: EventInstalled})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			tc.prepare(t, s)
			before, _ := s.Snapshot(installA)
			for _, ev := range []Event{EventHealthSuccess, EventCleanupDone, EventCleanupPending} {
				applyErr(t, s, installA, Input{Event: ev})
				after, _ := s.Snapshot(installA)
				if after != before {
					t.Fatalf("event %s mutated the model: %+v -> %+v", ev, before, after)
				}
				if after.HealthVerified || after.State == StateComplete || after.State == StateCleanup {
					t.Fatalf("health/cleanup claimed without passing health: %+v", after)
				}
			}
		})
	}
}

func TestDuplicateRequestIdempotentConflictingTargetRefused(t *testing.T) {
	s := NewStore()
	first := mustStart(t, s, installA, targetV)

	// Same target: idempotent, same operation.
	second, err := s.Start(installA, targetV)
	if err != nil {
		t.Fatalf("idempotent duplicate failed: %v", err)
	}
	if second.ID != first.ID || second.State != first.State || second.TargetVersion != first.TargetVersion {
		t.Fatalf("duplicate not idempotent: %+v vs %+v", second, first)
	}

	// Conflicting target: refused, existing operation unchanged.
	_, err = s.Start(installA, "0.10.0")
	if err == nil {
		t.Fatal("conflicting target unexpectedly accepted")
	}
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != MachineConflictingTarget {
		t.Fatalf("conflicting-target error = %v", err)
	}
	if snap, _ := s.Snapshot(installA); snap.ID != first.ID || snap.TargetVersion != targetV {
		t.Fatalf("refused request mutated the existing operation: %+v", snap)
	}

	// A different installation is independent.
	mustStart(t, s, installB, "0.10.0")
}

func TestTerminalRetryStartsNewDistinctOperation(t *testing.T) {
	s := NewStore()
	driveToWaiting(t, s, installA, targetV)
	mustApply(t, s, installA, Input{Event: EventCancel})

	first, _ := s.Snapshot(installA)
	if !first.State.Terminal() {
		t.Fatalf("setup: %q is not terminal", first.State)
	}

	// A new Start after a terminal state begins a distinct operation with
	// a new monotonic ID, back in checking, with clean flags.
	second, err := s.Start(installA, targetV)
	if err != nil {
		t.Fatalf("terminal retry failed: %v", err)
	}
	if second.ID <= first.ID || second.State != StateChecking {
		t.Fatalf("retry not fresh/distinct: %+v", second)
	}
	if second.ResumeAdmission || second.RecoveryRequested || second.RecoveryNeeded || second.ConsentRecorded ||
		second.HealthVerified || second.SuccessWithPendingCleanup || second.HasFailure {
		t.Fatalf("retry inherited stale flags: %+v", second)
	}

	// The active retry absorbs duplicates idempotently again.
	again, err := s.Start(installA, targetV)
	if err != nil || again.ID != second.ID {
		t.Fatalf("idempotency after retry: %+v err=%v", again, err)
	}

	// Drive the retry to a terminal state; only then does another Start
	// yield yet another distinct operation.
	mustApply(t, s, installA, Input{Event: EventCancel})
	third, err := s.Start(installA, targetV)
	if err != nil {
		t.Fatalf("second retry failed: %v", err)
	}
	if third.ID <= second.ID || third.State != StateChecking {
		t.Fatalf("retry not distinct: %+v", third)
	}
}

func TestExplicitRecoverNotComplete(t *testing.T) {
	s := NewStore()
	driveToWaiting(t, s, installA, targetV)
	mustApply(t, s, installA, Input{Event: EventDrain})
	mustApply(t, s, installA, Input{Event: EventDrained})
	mustApply(t, s, installA, failInput(MachineInstallFailed, "activation could not finish; your previous version is still in place"))

	// recovered is only reachable explicitly from failed.
	snap := mustApply(t, s, installA, Input{Event: EventRecover})
	if snap.State != StateRecovered {
		t.Fatalf("state = %q, want recovered", snap.State)
	}
	if !snap.State.Terminal() || snap.State == StateComplete {
		t.Fatalf("recovered must be terminal and distinct from complete: %+v", snap)
	}
	if snap.HealthVerified || snap.SuccessWithPendingCleanup {
		t.Fatalf("recovery must not claim success: %+v", snap)
	}
	// No completion or health edge exists from recovered; terminal.
	applyErr(t, s, installA, Input{Event: EventHealthSuccess})
	applyErr(t, s, installA, Input{Event: EventCleanupDone})
	applyErr(t, s, installA, Input{Event: EventCancel})
	// Terminal retry starts a new distinct operation.
	next, err := s.Start(installA, targetV)
	if err != nil || next.State != StateChecking {
		t.Fatalf("retry from recovered: %+v err=%v", next, err)
	}
}

func TestFailuresNeedMachineCodeAndUserReason(t *testing.T) {
	s := NewStore()
	mustStart(t, s, installA, targetV)
	mustApply(t, s, installA, Input{Event: EventReady})

	// Missing code or user reason is refused.
	applyErr(t, s, installA, Input{Event: EventFail})
	applyErr(t, s, installA, Input{Event: EventFail, Failure: Failure{Code: MachineVerificationFailed}})
	applyErr(t, s, installA, Input{Event: EventFail, Failure: Failure{UserReason: "no code"}})

	// A well-formed failure is recorded. The Error type carries only a
	// machine code and the caller-composed user reason; secrecy is the
	// caller's duty — the type is a plain string, not a sanitizing one.
	snap := mustApply(t, s, installA, failInput(MachineVerificationFailed, "the downloaded update failed verification and was not installed"))
	if snap.State != StateFailed || !snap.HasFailure {
		t.Fatalf("failure not recorded: %+v", snap)
	}
	if snap.Failure.Code != MachineVerificationFailed {
		t.Fatalf("failure code = %q", snap.Failure.Code)
	}
	if snap.ResumeAdmission {
		t.Fatal("failure is not a cancel; admission flag must not be set by fail")
	}

	// The Error message form exposes only code and user reason.
	e := &Error{Code: MachineDownloadFailed, UserReason: "user-facing phrase"}
	if e.Error() != "operation: download-failed: user-facing phrase" {
		t.Fatalf("error form = %q", e.Error())
	}
}

func TestValidationMissingOperationAndDeferred(t *testing.T) {
	s := NewStore()

	// Empty keying is refused.
	if _, err := s.Start("", targetV); err == nil {
		t.Fatal("empty installation id accepted")
	}
	if _, err := s.Start(installA, ""); err == nil {
		t.Fatal("empty target accepted")
	}

	// Events for unknown installations are refused.
	_, err := s.Apply(installA, Input{Event: EventReady})
	if err == nil {
		t.Fatal("event for unknown installation accepted")
	}
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != MachineMissingOperation {
		t.Fatalf("missing-operation error = %v", err)
	}

	// A deferred operation is resumable, not terminal, and still absorbs
	// idempotent duplicates; conflicting targets are still refused.
	driveToWaiting(t, s, installB, targetV)
	mustApply(t, s, installB, Input{Event: EventDefer})
	snap, err := s.Start(installB, targetV)
	if err != nil {
		t.Fatalf("duplicate against deferred failed: %v", err)
	}
	if snap.State != StateDeferred {
		t.Fatalf("duplicate against deferred: %+v", snap)
	}
	if _, err := s.Start(installB, "0.10.0"); err == nil {
		t.Fatal("conflicting target against deferred accepted")
	}
	resumed := mustApply(t, s, installB, Input{Event: EventResume})
	if resumed.State != StateReady || resumed.ResumeAdmission {
		t.Fatalf("resume: %+v", resumed)
	}
}

func TestSnapshotCallerCannotMutateModel(t *testing.T) {
	s := NewStore()
	driveToWaiting(t, s, installA, targetV)
	mustApply(t, s, installA, Input{Event: EventWorkActive})

	snap, _ := s.Snapshot(installA)

	// Mutate every field of the caller's copy.
	snap.State = StateComplete
	snap.ActiveWork = false
	snap.HealthVerified = true
	snap.SuccessWithPendingCleanup = true
	snap.ResumeAdmission = true
	snap.RecoveryRequested = true
	snap.RecoveryNeeded = true
	snap.ConsentRecorded = true
	snap.TargetVersion = "999.0.0"
	snap.ID = -1
	snap.Failure = Failure{Code: MachineRestartFailed, UserReason: "tampered"}
	snap.HasFailure = true

	after, _ := s.Snapshot(installA)
	if after.State != StateWaiting || !after.ActiveWork || after.HealthVerified ||
		after.SuccessWithPendingCleanup || after.ResumeAdmission ||
		after.RecoveryRequested || after.RecoveryNeeded || after.ConsentRecorded ||
		after.TargetVersion != targetV || after.ID <= 0 || after.HasFailure {
		t.Fatalf("caller mutation leaked into the model: %+v", after)
	}

	// Snapshots returned by Apply are equally isolated.
	mutated := mustApply(t, s, installA, Input{Event: EventIdle})
	mutated.State = StateComplete
	mutated.ActiveWork = true
	after, _ = s.Snapshot(installA)
	if after.State != StateWaiting || after.ActiveWork {
		t.Fatalf("apply-snapshot mutation leaked: %+v", after)
	}
}

func TestRaceConcurrentStartsNewTargetsRejects(t *testing.T) {
	s := NewStore()
	const n = 32
	targets := make([]string, n)
	for i := range targets {
		targets[i] = "1.0." + string(rune('a'+i))
	}

	var wg sync.WaitGroup
	results := make([]Snapshot, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.Start(installA, targets[i])
		}(i)
	}
	wg.Wait()

	wins := 0
	winnerIdx := -1
	for i := range errs {
		if errs[i] == nil {
			wins++
			winnerIdx = i
			continue
		}
		var opErr *Error
		if !errors.As(errs[i], &opErr) || opErr.Code != MachineConflictingTarget {
			t.Fatalf("loser %d error = %v, want conflicting-target", i, errs[i])
		}
	}
	if wins != 1 {
		t.Fatalf("wins = %d, want exactly 1", wins)
	}
	final, _ := s.Snapshot(installA)
	if final.TargetVersion != targets[winnerIdx] || final.State != StateChecking {
		t.Fatalf("final = %+v, want winner %q in checking", final, targets[winnerIdx])
	}
}

func TestRaceConcurrentAppliesNoIllegalActivation(t *testing.T) {
	s := NewStore()
	driveToWaiting(t, s, installA, targetV)
	mustApply(t, s, installA, Input{Event: EventWorkActive})
	id := currentID(t, s, installA)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Apply(installA, Input{Event: EventDrain, OperationID: id}) // must never win while work is active
			_, _ = s.Apply(installA, Input{Event: EventWorkActive, OperationID: id})
			_, _ = s.Apply(installA, Input{Event: EventDrain, OperationID: id})
		}()
	}
	wg.Wait()

	// Idle is never sent, so no drain can legally win: the operation must
	// still be waiting with active work, never activated.
	final, _ := s.Snapshot(installA)
	if final.State != StateWaiting || !final.ActiveWork {
		t.Fatalf("concurrent applies ended in %+v (illegal activation?)", final)
	}
}

func TestInjectedIDSequenceRefusesRegression(t *testing.T) {
	// 100 for the first start, then a regression (99) that must be
	// refused, then a valid 101 for the retry.
	seq := []int64{100, 99, 101}
	i := 0
	s := NewStore(WithIDSource(func() int64 {
		id := seq[i]
		i++
		return id
	}))

	first := mustStart(t, s, installA, targetV)
	if first.ID != 100 {
		t.Fatalf("injected ID = %d, want 100", first.ID)
	}
	driveToWaiting(t, s, installA, targetV)
	mustApply(t, s, installA, Input{Event: EventCancel}) // terminal, next Start allocates

	// The injected sequence regresses (99 <= 100): refused, no operation
	// replaced.
	if _, err := s.Start(installA, targetV); err == nil {
		t.Fatal("non-monotonic injected ID accepted")
	}
	if snap, _ := s.Snapshot(installA); snap.ID != first.ID {
		t.Fatalf("failed allocation mutated the operation: %+v", snap)
	}

	// A subsequent strictly-increasing value is accepted again.
	next, err := s.Start(installA, targetV)
	if err != nil || next.ID != 101 || next.State != StateChecking {
		t.Fatalf("retry after regression: %+v err=%v", next, err)
	}
}

// pathTo returns a legal event path from a fresh operation to target, plus
// whether the target is reachable in this table (complete/recovered are
// covered by dedicated tests instead).
func pathTo(target State) ([]Input, bool) {
	path := func(events ...Input) []Input { return events }
	switch target {
	case StateChecking:
		return nil, true
	case StateAnnounced:
		return path(Input{Event: EventAnnounce}), true
	case StateReady:
		return path(Input{Event: EventReady}), true
	case StateDownloading:
		return path(Input{Event: EventReady}, Input{Event: EventDownload}), true
	case StateVerifying:
		return path(Input{Event: EventReady}, Input{Event: EventDownload}, Input{Event: EventDownloaded}), true
	case StateWaiting:
		return path(Input{Event: EventReady}, Input{Event: EventDownload}, Input{Event: EventDownloaded}, Input{Event: EventVerified}), true
	case StateDraining:
		return path(Input{Event: EventReady}, Input{Event: EventDownload}, Input{Event: EventDownloaded}, Input{Event: EventVerified}, Input{Event: EventDrain}), true
	case StateInstalling:
		return path(Input{Event: EventReady}, Input{Event: EventDownload}, Input{Event: EventDownloaded}, Input{Event: EventVerified}, Input{Event: EventDrain}, Input{Event: EventDrained}), true
	case StateRestarting:
		return path(Input{Event: EventReady}, Input{Event: EventDownload}, Input{Event: EventDownloaded}, Input{Event: EventVerified}, Input{Event: EventDrain}, Input{Event: EventDrained}, Input{Event: EventInstalled}), true
	case StateHealthCheck:
		return path(Input{Event: EventReady}, Input{Event: EventDownload}, Input{Event: EventDownloaded}, Input{Event: EventVerified}, Input{Event: EventDrain}, Input{Event: EventDrained}, Input{Event: EventInstalled}, Input{Event: EventRestarted}), true
	case StateCleanup:
		return path(Input{Event: EventReady}, Input{Event: EventDownload}, Input{Event: EventDownloaded}, Input{Event: EventVerified}, Input{Event: EventDrain}, Input{Event: EventDrained}, Input{Event: EventInstalled}, Input{Event: EventRestarted}, Input{Event: EventHealthSuccess}), true
	case StateDeferred:
		return path(Input{Event: EventDefer}), true
	case StateCancelled:
		return path(Input{Event: EventCancel}), true
	case StateFailed:
		return path(failInput(MachineDownloadFailed, "synthetic")), true
	}
	return nil, false
}

func TestExhaustiveIllegalEdgesRefused(t *testing.T) {
	// For every reachable state, every event not listed as a legal edge
	// must be refused and leave the model unchanged: no illegal success and
	// no forced transitions of any kind.
	states := []State{
		StateChecking, StateAnnounced, StateReady, StateDownloading,
		StateVerifying, StateWaiting, StateDraining, StateInstalling,
		StateRestarting, StateHealthCheck, StateCleanup, StateDeferred,
		StateCancelled, StateFailed,
	}
	events := []Event{
		EventAnnounce, EventReady, EventDownload, EventDownloaded,
		EventVerified, EventWorkActive, EventIdle, EventDrain,
		EventCancelActiveWork, EventDrained, EventInstalled, EventRestarted,
		EventHealthSuccess, EventHealthFail, EventCleanupDone,
		EventCleanupPending, EventCancel, EventDefer, EventResume,
		EventFail, EventRecover, EventBackendRecovered, EventBackendFailed,
	}

	for _, from := range states {
		s := NewStore()
		mustStart(t, s, installA, targetV)
		path, ok := pathTo(from)
		if !ok {
			t.Fatalf("no test path for state %q", from)
		}
		for _, step := range path {
			mustApply(t, s, installA, step)
		}
		snap, _ := s.Snapshot(installA)
		if snap.State != from {
			t.Fatalf("setup reached %q, want %q", snap.State, from)
		}

		for _, ev := range events {
			if _, legal := transitions[from][ev]; legal {
				continue
			}
			// EventCancel inside the protected region is special-cased
			// behavior (recovery request), not a transition; covered
			// elsewhere.
			if ev == EventCancel && protectedFromCancel[from] {
				continue
			}
			in := Input{Event: ev, OperationID: snap.ID}
			if ev == EventFail {
				in.Failure = Failure{Code: MachineDownloadFailed, UserReason: "synthetic"}
			}
			before, _ := s.Snapshot(installA)
			_, err := s.Apply(installA, in)
			if err == nil {
				t.Fatalf("event %s from %q unexpectedly succeeded", ev, from)
			}
			var opErr *Error
			if !errors.As(err, &opErr) || opErr.Code != MachineIllegalTransition {
				t.Fatalf("event %s from %q: wrong refusal %v", ev, from, err)
			}
			after, _ := s.Snapshot(installA)
			if before != after {
				t.Fatalf("refused event %s from %q mutated the model: %+v -> %+v", ev, from, before, after)
			}
		}
	}
}

func TestRecordSchemaJSONRoundTrip(t *testing.T) {
	// Record is a design deliverable only: verified as an in-memory JSON
	// schema. Nothing in this package writes any file.
	s := NewStore()
	driveToInstalling(t, s, installA, targetV)
	mustApply(t, s, installA, Input{Event: EventInstalled})
	mustApply(t, s, installA, Input{Event: EventRestarted})
	mustApply(t, s, installA, Input{Event: EventHealthSuccess})
	snap := mustApply(t, s, installA, Input{Event: EventCleanupPending})

	// JSON preserves instants, not monotonic readings or Location pointer identity.
	want := snap.Record()
	blob, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	var back Record
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	if !back.CreatedAt.Equal(want.CreatedAt) || !back.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("record timestamps changed: got %v/%v want %v/%v", back.CreatedAt, back.UpdatedAt, want.CreatedAt, want.UpdatedAt)
	}
	// Having checked both instants, compare every remaining field exactly.
	want.CreatedAt, want.UpdatedAt = back.CreatedAt, back.UpdatedAt
	if back != want {
		t.Fatalf("record round trip mismatch:\n got %+v\nwant %+v", back, want)
	}
	if back.SchemaVersion != RecordSchemaVersion || back.State != StateComplete ||
		!back.SuccessWithPendingCleanup || !back.HealthVerified ||
		back.FailureCode != "" {
		t.Fatalf("record content = %+v", back)
	}

	// A failed operation serializes its failure code and user reason, and
	// nothing else (no raw detail field exists in the schema).
	s2 := NewStore()
	mustStart(t, s2, installB, targetV)
	snap = mustApply(t, s2, installB, failInput(MachineVerificationFailed, "user-facing phrase"))
	blob, err = json.Marshal(snap.Record())
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	var fback Record
	if err := json.Unmarshal(blob, &fback); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	if fback.FailureCode != string(MachineVerificationFailed) ||
		fback.FailureUserReason != "user-facing phrase" {
		t.Fatalf("failed record = %+v", fback)
	}
}

// TestRegressionStaleOperationIDCannotAdvanceNewOperation is the regression
// probe for explicit operation keying: a stale event carrying the previous
// (cancelled) operation's ID, and a zero ID, must both be refused and leave
// the current operation unchanged. There is no silent default-current-ID
// path.
func TestRegressionStaleOperationIDCannotAdvanceNewOperation(t *testing.T) {
	s := NewStore()
	first := mustStart(t, s, installA, targetV)
	mustApply(t, s, installA, Input{Event: EventReady})
	mustApply(t, s, installA, Input{Event: EventCancel}) // terminal; stale from here on
	staleID := first.ID

	second := mustStart(t, s, installA, targetV)
	if second.ID == staleID {
		t.Fatal("setup: retry did not allocate a new ID")
	}
	before, _ := s.Snapshot(installA)

	// Stale ID: refused as stale-operation, model unchanged — even though
	// EventReady would be a legal edge for the new operation. The stale ID
	// is carried deliberately, not via the ID-attaching helpers.
	_, staleErr := s.Apply(installA, Input{Event: EventReady, OperationID: staleID})
	if staleErr == nil {
		t.Fatal("stale-ID apply unexpectedly succeeded")
	}
	var code *Error
	if !errors.As(staleErr, &code) || code.Code != MachineStaleOperation {
		t.Fatalf("stale ID error = %v, want stale-operation", staleErr)
	}
	after, _ := s.Snapshot(installA)
	if after != before {
		t.Fatalf("stale event mutated the current operation: %+v -> %+v", before, after)
	}

	// Zero ID: refused as operation-id-required, model unchanged. No
	// default-to-current path exists.
	_, zeroErr := s.Apply(installA, Input{Event: EventReady, OperationID: 0})
	if zeroErr == nil {
		t.Fatal("zero-ID apply unexpectedly succeeded")
	}
	if !errors.As(zeroErr, &code) || code.Code != MachineOperationIDRequired {
		t.Fatalf("zero ID error = %v, want operation-id-required", zeroErr)
	}
	after, _ = s.Snapshot(installA)
	if after != before {
		t.Fatalf("zero-ID event mutated the current operation: %+v -> %+v", before, after)
	}

	// A stale cancel inside the protected region must not even record a
	// recovery request on the new operation.
	driveToInstalling(t, s, installA, targetV)
	before, _ = s.Snapshot(installA)
	_, applyErr2 := s.Apply(installA, Input{Event: EventCancel, OperationID: staleID})
	if applyErr2 == nil {
		t.Fatal("stale cancel in protected region accepted")
	}
	var staleCancel *Error
	if !errors.As(applyErr2, &staleCancel) || staleCancel.Code != MachineStaleOperation {
		t.Fatalf("stale cancel error = %v, want stale-operation", applyErr2)
	}
	after, _ = s.Snapshot(installA)
	if after != before || after.RecoveryRequested {
		t.Fatalf("stale cancel mutated the current operation: %+v -> %+v", before, after)
	}

	// The current ID still works.
	snap2 := mustApply(t, s, installA, Input{Event: EventInstalled})
	if snap2.State != StateRestarting {
		t.Fatalf("current-ID apply: state = %q, want restarting", snap2.State)
	}
}

// TestRegressionCancelActiveWorkStaysActiveUntilIdle is the regression probe
// for the consent/draining semantics: consenting to cancel active work
// begins draining, but the work is still active until the explicit idle
// callback; drained refuses while active, and idle is legal in draining.
func TestRegressionCancelActiveWorkStaysActiveUntilIdle(t *testing.T) {
	s := NewStore()
	driveToWaiting(t, s, installA, targetV)
	mustApply(t, s, installA, Input{Event: EventWorkActive})

	snap := mustApply(t, s, installA, Input{Event: EventCancelActiveWork, Consent: true})
	if snap.State != StateDraining {
		t.Fatalf("consented cancel: state = %q, want draining", snap.State)
	}
	if !snap.ConsentRecorded {
		t.Fatal("consent must be recorded")
	}
	if !snap.ActiveWork {
		t.Fatal("consent must not silently clear active work; only the explicit idle callback may")
	}

	// Draining is not finished: drained must refuse while active.
	before, _ := s.Snapshot(installA)
	err := applyErr(t, s, installA, Input{Event: EventDrained})
	if err.Code != MachineActiveWorkPresent {
		t.Fatalf("drained-while-active code = %q, want active-work-present", err.Code)
	}
	after, _ := s.Snapshot(installA)
	if after != before {
		t.Fatalf("refused drained mutated the model: %+v -> %+v", before, after)
	}

	// The explicit idle callback is legal in draining and clears the work.
	snap = mustApply(t, s, installA, Input{Event: EventIdle})
	if snap.State != StateDraining || snap.ActiveWork {
		t.Fatalf("idle in draining: %+v", snap)
	}

	// Only now does drained pass.
	snap = mustApply(t, s, installA, Input{Event: EventDrained})
	if snap.State != StateInstalling {
		t.Fatalf("drained after idle: state = %q, want installing", snap.State)
	}
}

// TestRegressionHealthFailureRecordsSafeDetails is the regression probe for
// health failure details: EventHealthFail must land in failed with a safe
// machine code and a fixed, sanitized user reason (never caller detail),
// and the failure is protected-region so recovery is explicit.
func TestRegressionHealthFailureRecordsSafeDetails(t *testing.T) {
	s := NewStore()
	driveToInstalling(t, s, installA, targetV)
	mustApply(t, s, installA, Input{Event: EventInstalled})
	mustApply(t, s, installA, Input{Event: EventRestarted})

	snap := mustApply(t, s, installA, Input{Event: EventHealthFail})
	if snap.State != StateFailed {
		t.Fatalf("health-fail: state = %q, want failed", snap.State)
	}
	if !snap.HasFailure {
		t.Fatal("health failure must record failure details")
	}
	if snap.Failure.Code != MachineHealthCheckFailed {
		t.Fatalf("health-fail code = %q, want health-check-failed", snap.Failure.Code)
	}
	if snap.Failure.UserReason == "" || snap.Failure.UserReason != healthFailUserReason {
		t.Fatalf("health-fail reason = %q, want the fixed sanitized phrase", snap.Failure.UserReason)
	}
	if !snap.RecoveryNeeded {
		t.Fatal("a failure during health verification is a protected failure; recovery must be explicit")
	}
}

// TestRegressionRecoveryRequestedFreezesAdvancement is the regression probe
// for the freeze: after a cancel is recorded inside the protected region, no
// ordinary event may advance the operation (not even the normal
// installed/restarted/health-success path to complete); only the backend's
// explicit safe outcome resolves it.
func TestRegressionRecoveryRequestedFreezesAdvancement(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, s *Store)
	}{
		{"installing", func(t *testing.T, s *Store) { driveToInstalling(t, s, installA, targetV) }},
		{"restarting", func(t *testing.T, s *Store) {
			driveToInstalling(t, s, installA, targetV)
			mustApply(t, s, installA, Input{Event: EventInstalled})
		}},
		{"healthcheck", func(t *testing.T, s *Store) {
			driveToInstalling(t, s, installA, targetV)
			mustApply(t, s, installA, Input{Event: EventInstalled})
			mustApply(t, s, installA, Input{Event: EventRestarted})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			tc.prepare(t, s)
			snap, err := s.Apply(installA, Input{Event: EventCancel, OperationID: currentID(t, s, installA)})
			if err != nil || !snap.RecoveryRequested {
				t.Fatalf("setup: cancel must record recovery-requested: %+v err=%v", snap, err)
			}
			before, _ := s.Snapshot(installA)

			frozen := []Input{
				{Event: EventInstalled},
				{Event: EventRestarted},
				{Event: EventHealthSuccess},
				{Event: EventHealthFail},
				{Event: EventCleanupDone},
				{Event: EventFail, Failure: Failure{Code: MachineInstallFailed, UserReason: "synthetic"}},
				{Event: EventCancel},
				{Event: EventDefer},
				{Event: EventResume},
				{Event: EventDrained},
			}
			for _, in := range frozen {
				in.OperationID = before.ID
				err := applyErr(t, s, installA, in)
				if err.Code != MachineRecoveryPending {
					t.Fatalf("frozen event %s: code = %q, want recovery-pending", in.Event, err.Code)
				}
				after, _ := s.Snapshot(installA)
				if after != before {
					t.Fatalf("frozen event %s mutated the model: %+v -> %+v", in.Event, before, after)
				}
			}

			// The explicit backend safe outcome resolves it.
			snap = mustApply(t, s, installA, Input{Event: EventBackendRecovered})
			if snap.State != StateRecovered || snap.RecoveryRequested {
				t.Fatalf("backend-recovered: %+v", snap)
			}
		})
	}
}

// TestRegressionFailedProtectedUpdateBlocksStart is the regression probe for
// terminal-state discipline: an operation that failed during activation
// cannot be replaced by a new Start until the backend confirms a safe failed
// state (backend-failed) or a successful rollback (backend-recovered, or
// the explicit recover). A failure before activation stays an ordinary
// terminal failure and retry remains free. There is no implicit recovered.
func TestRegressionFailedProtectedUpdateBlocksStart(t *testing.T) {
	// Protected failure: blocked until the backend confirms.
	s := NewStore()
	driveToInstalling(t, s, installA, targetV)
	snap := mustApply(t, s, installA, failInput(MachineInstallFailed, "activation could not finish; your previous version is still in place"))
	if snap.State != StateFailed || !snap.RecoveryNeeded {
		t.Fatalf("protected failure: %+v", snap)
	}

	before, _ := s.Snapshot(installA)
	for _, target := range []string{targetV, "0.10.0"} {
		_, err := s.Start(installA, target)
		if err == nil {
			t.Fatalf("Start(%q) after protected failure unexpectedly allowed", target)
		}
		var opErr *Error
		if !errors.As(err, &opErr) || opErr.Code != MachineRecoveryNeeded {
			t.Fatalf("blocked Start error = %v, want recovery-needed", err)
		}
	}
	after, _ := s.Snapshot(installA)
	if after != before {
		t.Fatalf("blocked Start mutated the failed operation: %+v -> %+v", before, after)
	}

	// Explicit rollback resolves it; only then may a new operation start.
	snap = mustApply(t, s, installA, Input{Event: EventRecover})
	if snap.State != StateRecovered || snap.RecoveryNeeded {
		t.Fatalf("explicit recover: %+v", snap)
	}
	next, err := s.Start(installA, targetV)
	if err != nil || next.State != StateChecking || next.ID <= before.ID {
		t.Fatalf("retry after recover: %+v err=%v", next, err)
	}

	// Backend-confirmed safe failed state also unblocks.
	s2 := NewStore()
	driveToInstalling(t, s2, installA, targetV)
	mustApply(t, s2, installA, failInput(MachineInstallFailed, "activation could not finish; your previous version is still in place"))
	snap = mustApply(t, s2, installA, Input{Event: EventBackendFailed})
	if snap.State != StateFailed || snap.RecoveryNeeded || !snap.HasFailure {
		t.Fatalf("backend-failed confirmation: %+v", snap)
	}
	if _, err := s2.Start(installA, targetV); err != nil {
		t.Fatalf("Start after backend-confirmed safe failure: %v", err)
	}

	// Backend-recovered (successful rollback) also unblocks.
	s3 := NewStore()
	driveToInstalling(t, s3, installA, targetV)
	mustApply(t, s3, installA, failInput(MachineInstallFailed, "activation could not finish; your previous version is still in place"))
	snap = mustApply(t, s3, installA, Input{Event: EventBackendRecovered})
	if snap.State != StateRecovered || snap.RecoveryNeeded {
		t.Fatalf("backend-recovered rollback: %+v", snap)
	}
	if _, err := s3.Start(installA, targetV); err != nil {
		t.Fatalf("Start after backend rollback: %v", err)
	}

	// A failure before activation is an ordinary terminal failure: retry
	// stays free, and no recovery-needed flag appears.
	s4 := NewStore()
	mustStart(t, s4, installB, targetV)
	mustApply(t, s4, installB, Input{Event: EventReady})
	snap = mustApply(t, s4, installB, failInput(MachineVerificationFailed, "the downloaded update failed verification and was not installed"))
	if snap.State != StateFailed || snap.RecoveryNeeded {
		t.Fatalf("pre-activation failure must be terminal without recovery-needed: %+v", snap)
	}
	if _, err := s4.Start(installB, targetV); err != nil {
		t.Fatalf("retry after pre-activation failure: %v", err)
	}
}
