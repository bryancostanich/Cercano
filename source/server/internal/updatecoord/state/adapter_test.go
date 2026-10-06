package state

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"cercano/source/server/internal/updatecoord/operation"
)

// Adapter tests run ONLY against temporary state directories and the pure
// model; they touch no real installation, probe, RPC, or UI surface.

func openAdapter(t *testing.T, root string) (*Store, *Adapter) {
	t.Helper()
	s, err := Open(root, "test-install")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, NewAdapter(s)
}

func mustStart(t *testing.T, a *Adapter, target string) operation.Snapshot {
	t.Helper()
	snap, err := a.Start(context.Background(), target)
	if err != nil {
		t.Fatalf("adapter Start(%q): %v", target, err)
	}
	return snap
}

func currentSnapshot(t *testing.T, a *Adapter) operation.Snapshot {
	t.Helper()
	snap, ok, err := a.Snapshot(context.Background())
	if err != nil || !ok {
		t.Fatalf("adapter Snapshot: ok=%v err=%v", ok, err)
	}
	return snap
}

func mustApply(t *testing.T, a *Adapter, in operation.Input) operation.Snapshot {
	t.Helper()
	in.OperationID = currentSnapshot(t, a).ID
	snap, err := a.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("adapter Apply(%s): %v", in.Event, err)
	}
	return snap
}

func applyRefusal(t *testing.T, a *Adapter, in operation.Input) *operation.Error {
	t.Helper()
	if in.OperationID == 0 {
		// Only unnamed events are keyed to the current operation; an
		// explicit ID (for example a stale callback) is preserved as-is.
		if snap, ok, err := a.Snapshot(context.Background()); err == nil && ok {
			in.OperationID = snap.ID
		}
	}
	_, err := a.Apply(context.Background(), in)
	if err == nil {
		t.Fatalf("adapter Apply(%s): unexpectedly succeeded", in.Event)
	}
	var opErr *operation.Error
	if !errors.As(err, &opErr) {
		t.Fatalf("adapter Apply error is %T, want *operation.Error: %v", err, err)
	}
	return opErr
}

func startRefusal(t *testing.T, a *Adapter, target string) *operation.Error {
	t.Helper()
	_, err := a.Start(context.Background(), target)
	if err == nil {
		t.Fatalf("adapter Start(%q): unexpectedly succeeded", target)
	}
	var opErr *operation.Error
	if !errors.As(err, &opErr) {
		t.Fatalf("adapter Start error is %T, want *operation.Error: %v", err, err)
	}
	return opErr
}

// driveToInstalling walks the pre-activation lifecycle through the adapter.
func driveToInstalling(t *testing.T, a *Adapter, target string) operation.Snapshot {
	t.Helper()
	mustStart(t, a, target)
	mustApply(t, a, operation.Input{Event: operation.EventReady})
	mustApply(t, a, operation.Input{Event: operation.EventDownload})
	mustApply(t, a, operation.Input{Event: operation.EventDownloaded})
	mustApply(t, a, operation.Input{Event: operation.EventVerified})
	mustApply(t, a, operation.Input{Event: operation.EventDrain})
	return mustApply(t, a, operation.Input{Event: operation.EventDrained})
}

func recordCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM operation_records`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// peekNextID reads the persisted identifier counter WITHOUT allocating, so
// the test can assert what a refused path burned (or did not) without
// consuming an identifier itself.
func peekNextID(t *testing.T, s *Store) int64 {
	t.Helper()
	var next int64
	if err := s.db.QueryRow(`SELECT next_op_id FROM install_state WHERE install_id = ?`, s.installID).Scan(&next); err != nil {
		t.Fatal(err)
	}
	return next
}

// TestAdapterReopenResume persists a mid-flight operation, reopens the
// store, and resumes it through the same adapter: the current operation,
// its identifier, target, and creation time survive, and continuing events
// advance the persisted record.
func TestAdapterReopenResume(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, a := openAdapter(t, root)
	snap := mustStart(t, a, "1.2.3")
	mustApply(t, a, operation.Input{Event: operation.EventReady})
	mustApply(t, a, operation.Input{Event: operation.EventDownload})
	mustApply(t, a, operation.Input{Event: operation.EventDownloaded})
	mustApply(t, a, operation.Input{Event: operation.EventVerified})
	mustApply(t, a, operation.Input{Event: operation.EventDefer})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, a = openAdapter(t, root)
	defer s.Close()
	resumed, ok, err := a.Snapshot(ctx)
	if err != nil || !ok {
		t.Fatalf("resume snapshot: ok=%v err=%v", ok, err)
	}
	if resumed.ID != snap.ID || resumed.State != operation.StateDeferred ||
		resumed.TargetVersion != "1.2.3" || !resumed.CreatedAt.Equal(snap.CreatedAt) {
		t.Fatalf("resumed %+v, started %+v", resumed, snap)
	}
	after := mustApply(t, a, operation.Input{Event: operation.EventResume})
	if after.State != operation.StateReady {
		t.Fatalf("resume after reopen produced %s", after.State)
	}
	// The revision advanced with the event, in the same transaction.
	_, revision, err := s.LoadOperationRecord(ctx, resumed.ID)
	if err != nil || revision != 7 {
		t.Fatalf("revision after resume: %d %v", revision, err)
	}
	// A same-target start against the still-active resumed operation is
	// idempotent: it returns the current operation without burning an ID.
	idem := mustStart(t, a, "1.2.3")
	if idem.ID != resumed.ID || idem.State != operation.StateReady {
		t.Fatalf("idempotent start returned %+v", idem)
	}
	if peekNextID(t, s) != 2 {
		t.Fatal("idempotent start burned an identifier")
	}
}

// TestAdapterSameTargetDedupAcrossHandles: two handles on the same
// installation observe each other's active operation; the same target is
// deduplicated to the current operation and a conflicting target is
// refused, with exactly one record and one allocated identifier.
func TestAdapterSameTargetDedupAcrossHandles(t *testing.T) {
	root := t.TempDir()
	s1, a1 := openAdapter(t, root)
	defer s1.Close()
	s2, a2 := openAdapter(t, root)
	defer s2.Close()

	first := mustStart(t, a1, "2.0.0")
	second := mustStart(t, a2, "2.0.0")
	if second.ID != first.ID || second.State != first.State {
		t.Fatalf("cross-handle dedup failed: %+v vs %+v", second, first)
	}
	if n := recordCount(t, s2); n != 1 {
		t.Fatalf("dedup wrote %d records", n)
	}
	if opErr := startRefusal(t, a1, "3.0.0"); opErr.Code != operation.MachineConflictingTarget {
		t.Fatalf("conflicting target refusal: %v", opErr.Code)
	}
	if n := recordCount(t, s2); n != 1 {
		t.Fatalf("refused start wrote records: %d", n)
	}
	if peekNextID(t, s2) != 2 {
		t.Fatal("dedup or refusal burned identifiers")
	}
}

// TestAdapterStaleCallbackCannotMutateReplacementAcrossReopen: an event
// naming a superseded operation's ID — including after the store was
// closed and reopened and a replacement operation started — is refused
// (stale-operation), and the replacement is not mutated. A zero ID is
// refused as operation-id-required.
func TestAdapterStaleCallbackCannotMutateReplacementAcrossReopen(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, a := openAdapter(t, root)
	first := mustStart(t, a, "1.0.0")
	cancelled := mustApply(t, a, operation.Input{Event: operation.EventCancel})
	if cancelled.State != operation.StateCancelled {
		t.Fatal("pre-activation cancel did not cancel")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, a = openAdapter(t, root)
	defer s.Close()
	second := mustStart(t, a, "2.0.0")
	if second.ID == first.ID {
		t.Fatal("replacement reused the superseded operation's identifier")
	}
	stale := operation.Input{Event: operation.EventAnnounce, OperationID: first.ID}
	opErr := applyRefusal(t, a, stale)
	if opErr.Code != operation.MachineStaleOperation {
		t.Fatalf("stale callback refusal: %v", opErr.Code)
	}
	// A callback that names NO operation is refused before anything else.
	if _, err := a.Apply(ctx, operation.Input{Event: operation.EventAnnounce}); err == nil {
		t.Fatal("zero-ID Apply unexpectedly succeeded")
	} else {
		var zeroErr *operation.Error
		if !errors.As(err, &zeroErr) || zeroErr.Code != operation.MachineOperationIDRequired {
			t.Fatalf("zero-ID refusal: %v", err)
		}
	}
	current := currentSnapshot(t, a)
	if current.ID != second.ID || current.State != operation.StateChecking {
		t.Fatalf("replacement mutated by stale callbacks: %+v", current)
	}
	// The superseded operation's persisted history is immutable.
	old, _, err := s.LoadOperationRecord(ctx, first.ID)
	if err != nil || old.State != operation.StateCancelled {
		t.Fatalf("history rewritten: %+v %v", old, err)
	}
	// The current replacement still advances normally.
	announced := mustApply(t, a, operation.Input{Event: operation.EventAnnounce})
	if announced.State != operation.StateAnnounced {
		t.Fatalf("replacement blocked by stale callbacks: %+v", announced)
	}
}

// TestAdapterConcurrentStartTwoHandlesExactlyOneOperation: concurrent
// starts of the same target on two handles observe each other through the
// single BEGIN IMMEDIATE transaction; exactly one operation record and one
// identifier exist, and every caller sees the same operation. Concurrent
// starts of DIFFERENT targets produce exactly one operation and one
// conflicting-target refusal.
func TestAdapterConcurrentStartTwoHandlesExactlyOneOperation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s1, a1 := openAdapter(t, root)
	defer s1.Close()
	s2, a2 := openAdapter(t, root)
	defer s2.Close()

	const n = 8
	var wg sync.WaitGroup
	snaps := make(chan operation.Snapshot, n)
	errs := make(chan *operation.Error, n)
	adapters := []*Adapter{a1, a2}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(a *Adapter) {
			defer wg.Done()
			snap, err := a.Start(ctx, "2.0.0")
			if err != nil {
				var opErr *operation.Error
				if !errors.As(err, &opErr) {
					opErr = &operation.Error{Code: "not-an-operation-error"}
				}
				errs <- opErr
				return
			}
			snaps <- snap
		}(adapters[i%2])
	}
	wg.Wait()
	close(snaps)
	close(errs)
	for opErr := range errs {
		t.Errorf("concurrent same-target start refused: %+v", opErr)
	}
	distinct := map[int64]bool{}
	for snap := range snaps {
		distinct[snap.ID] = true
	}
	if len(distinct) != 1 {
		t.Fatalf("concurrent starts produced %d distinct operations", len(distinct))
	}
	if n := recordCount(t, s1); n != 1 {
		t.Fatalf("concurrent starts wrote %d records", n)
	}
	if peekNextID(t, s1) != 2 {
		t.Fatal("concurrent starts burned identifiers")
	}

	// Conflicting concurrent starts: finish the current operation first,
	// then race two DIFFERENT targets: exactly one start commits, and the
	// loser observes the winner's active operation and is refused.
	mustApply(t, a1, operation.Input{Event: operation.EventCancel})
	var winners sync.WaitGroup
	results := make(chan *operation.Error, 2)
	targets := []string{"3.0.0", "4.0.0"}
	racers := []*Adapter{a1, a2}
	for i, target := range targets {
		winners.Add(1)
		go func(target string, a *Adapter) {
			defer winners.Done()
			_, err := a.Start(ctx, target)
			if err == nil {
				results <- nil
				return
			}
			var opErr *operation.Error
			if !errors.As(err, &opErr) {
				opErr = &operation.Error{Code: "not-an-operation-error"}
			}
			results <- opErr
		}(target, racers[i])
	}
	winners.Wait()
	close(results)
	succeeded, refused := 0, 0
	for opErr := range results {
		if opErr == nil {
			succeeded++
			continue
		}
		if opErr.Code != operation.MachineConflictingTarget {
			t.Fatalf("conflicting concurrent start refused with %v", opErr.Code)
		}
		refused++
	}
	if succeeded != 1 || refused != 1 {
		t.Fatalf("conflicting starts: %d succeeded, %d refused", succeeded, refused)
	}
	if n := recordCount(t, s1); n != 2 {
		t.Fatalf("conflicting starts wrote %d records", n)
	}
	if peekNextID(t, s1) != 3 {
		t.Fatal("conflicting starts burned extra identifiers")
	}
}

// TestAdapterPreactivationCancellationFreesNewStart: a cancel before
// activation is a terminal outcome; it persists, frees a new start, and the
// cancelled history record is never rewritten.
func TestAdapterPreactivationCancellationFreesNewStart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, a := openAdapter(t, root)
	defer s.Close()
	first := mustStart(t, a, "1.0.0")
	cancelled := mustApply(t, a, operation.Input{Event: operation.EventCancel})
	if !cancelled.State.Terminal() {
		t.Fatalf("pre-activation cancel is not terminal: %s", cancelled.State)
	}
	persisted, _, err := s.LoadOperationRecord(ctx, first.ID)
	if err != nil || persisted.State != operation.StateCancelled {
		t.Fatalf("cancel not persisted: %+v %v", persisted, err)
	}
	second := mustStart(t, a, "1.1.0")
	if second.ID == first.ID || second.State != operation.StateChecking {
		t.Fatalf("terminal cancel did not free a fresh start: %+v", second)
	}
	if n := recordCount(t, s); n != 2 {
		t.Fatalf("expected history plus current, got %d records", n)
	}
	// History stays immutable once a newer operation starts.
	mustApply(t, a, operation.Input{Event: operation.EventReady})
	persisted, _, err = s.LoadOperationRecord(ctx, first.ID)
	if err != nil || persisted.State != operation.StateCancelled {
		t.Fatalf("history rewritten after new start: %+v %v", persisted, err)
	}
}

// TestAdapterProtectedRecoveryBlocksAndResolves: a cancel inside the
// protected region records RecoveryRequested and freezes ordinary events;
// a failure in the protected region blocks a restart (recovery-needed)
// until the backend resolves it. Only then may a new operation start.
func TestAdapterProtectedRecoveryBlocksAndResolves(t *testing.T) {
	root := t.TempDir()
	s, a := openAdapter(t, root)
	defer s.Close()

	// Cancel in the protected region: recorded, frozen, resolved by the
	// backend, then a new start is allowed.
	driveToInstalling(t, a, "2.0.0")
	requested := mustApply(t, a, operation.Input{Event: operation.EventCancel})
	if requested.State != operation.StateInstalling || !requested.RecoveryRequested {
		t.Fatalf("protected cancel not recorded as a recovery request: %+v", requested)
	}
	if opErr := applyRefusal(t, a, operation.Input{Event: operation.EventInstalled}); opErr.Code != operation.MachineRecoveryPending {
		t.Fatalf("frozen region advanced: %v", opErr.Code)
	}
	failed := mustApply(t, a, operation.Input{Event: operation.EventBackendFailed})
	if failed.State != operation.StateFailed || failed.RecoveryRequested {
		t.Fatalf("backend-failed did not resolve the request: %+v", failed)
	}
	if next := mustStart(t, a, "2.1.0"); next.ID != 2 {
		t.Fatalf("resolved cancel did not free a new start: %+v", next)
	}
	if peekNextID(t, s) != 3 {
		t.Fatal("unexpected identifier consumption in protected-cancel flow")
	}

	// A failure in the protected region blocks restart until recovery.
	root2 := t.TempDir()
	s2, a2 := openAdapter(t, root2)
	defer s2.Close()
	driveToInstalling(t, a2, "3.0.0")
	failed = mustApply(t, a2, operation.Input{Event: operation.EventFail, Failure: operation.Failure{Code: operation.MachineInstallFailed, UserReason: "the package transaction failed"}})
	if failed.State != operation.StateFailed || !failed.RecoveryNeeded {
		t.Fatalf("protected failure did not record recovery-needed: %+v", failed)
	}
	if opErr := startRefusal(t, a2, "3.1.0"); opErr.Code != operation.MachineRecoveryNeeded {
		t.Fatalf("protected failure did not block restart: %v", opErr.Code)
	}
	if peekNextID(t, s2) != 2 {
		t.Fatal("blocked restart burned an identifier")
	}
	if n := recordCount(t, s2); n != 1 {
		t.Fatalf("blocked restart wrote records: %d", n)
	}
	recovered := mustApply(t, a2, operation.Input{Event: operation.EventRecover})
	if recovered.State != operation.StateRecovered || recovered.RecoveryNeeded {
		t.Fatalf("explicit recover did not clear recovery-needed: %+v", recovered)
	}
	if next := mustStart(t, a2, "3.1.0"); next.ID != 2 {
		t.Fatalf("recovered operation did not free a new start: %+v", next)
	}
}

// TestAdapterCounterRollbackOnAbortedStart: an injected fault between the
// counter allocation and the record insert aborts the whole Start
// transaction; the identifier counter rolls back, no record exists, and no
// identifier gap is burned.
func TestAdapterCounterRollbackOnAbortedStart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, a := openAdapter(t, root)
	defer s.Close()
	calls := 0
	s.fault = func() error {
		calls++
		if calls == 2 {
			return errors.New("fixture abort between counter and record")
		}
		return nil
	}
	if _, err := a.Start(ctx, "1.0.0"); err == nil {
		t.Fatal("faulted start succeeded")
	}
	s.fault = nil
	if n := recordCount(t, s); n != 0 {
		t.Fatalf("aborted start persisted %d records", n)
	}
	if _, _, err := s.LoadOperationRecord(ctx, 1); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("aborted start persisted a record: %v", err)
	}
	if peekNextID(t, s) != 1 {
		t.Fatal("aborted start burned an identifier gap")
	}
	if snap := mustStart(t, a, "1.0.0"); snap.ID != 1 {
		t.Fatalf("aborted start burned an identifier gap: %+v", snap)
	}
	if peekNextID(t, s) != 2 {
		t.Fatal("successful start did not advance the counter exactly once")
	}
}

// TestAdapterInvalidRecordRefusedWithoutMutation: a persisted record that
// violates the model's own invariants (here: health verification claimed in
// a state the model can never record it in) is refused by every adapter
// path — Start, Apply, and Snapshot — without any mutation: the record is
// not reset, no new operation is created, and the counter is untouched.
func TestAdapterInvalidRecordRefusedWithoutMutation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, a := openAdapter(t, root)
	defer s.Close()
	mustStart(t, a, "1.0.0")
	mustApply(t, a, operation.Input{Event: operation.EventReady})

	rec, revision, err := s.LoadOperationRecord(ctx, 1)
	if err != nil || revision != 2 {
		t.Fatalf("load before tampering: rev=%d err=%v", revision, err)
	}
	// Valid canonical JSON, but a flag the pure model can never produce in
	// this state: restore must refuse it, never silently reset it.
	rec.HealthVerified = true
	payload, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE operation_records SET record_json = ? WHERE op_id = 1`, string(payload)); err != nil {
		t.Fatal(err)
	}
	before, _, err := s.LoadOperationRecord(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = a.Start(ctx, "2.0.0"); !errors.Is(err, ErrCorruptDatabase) {
		t.Fatalf("Start accepted an invariant-violating record: %v", err)
	}
	if _, err = a.Apply(ctx, operation.Input{Event: operation.EventDownload, OperationID: 1}); !errors.Is(err, ErrCorruptDatabase) {
		t.Fatalf("Apply accepted an invariant-violating record: %v", err)
	}
	if _, _, err = a.Snapshot(ctx); !errors.Is(err, ErrCorruptDatabase) {
		t.Fatalf("Snapshot accepted an invariant-violating record: %v", err)
	}
	after, _, err := s.LoadOperationRecord(ctx, 1)
	if err != nil || after != before {
		t.Fatalf("invalid record was mutated or reset: %+v vs %+v (%v)", after, before, err)
	}
	if n := recordCount(t, s); n != 1 {
		t.Fatalf("refused adapter paths wrote records: %d", n)
	}
	if peekNextID(t, s) != 2 {
		t.Fatal("refused adapter paths burned identifiers")
	}
}

// TestAdapterReachableRecoveredWithHealthSurvivesReopen: health
// verification survives a cleanup failure and the explicit recover, so the
// persisted recovered-with-health record is a snapshot the pure model can
// actually produce. The restore gate must accept it on reopen — never
// report a corrupt database — and preserve every field.
func TestAdapterReachableRecoveredWithHealthSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, a := openAdapter(t, root)
	driveToInstalling(t, a, "2.0.0")
	mustApply(t, a, operation.Input{Event: operation.EventInstalled})
	mustApply(t, a, operation.Input{Event: operation.EventRestarted})
	mustApply(t, a, operation.Input{Event: operation.EventHealthSuccess})
	mustApply(t, a, operation.Input{Event: operation.EventFail, Failure: operation.Failure{
		Code:       operation.MachineCleanupFailed,
		UserReason: "the superseded files could not be removed",
	}})
	recovered := mustApply(t, a, operation.Input{Event: operation.EventRecover})
	if recovered.State != operation.StateRecovered || !recovered.HealthVerified || !recovered.HasFailure {
		t.Fatalf("setup: recovered-with-health = %+v", recovered)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, a = openAdapter(t, root)
	defer s.Close()
	resumed, ok, err := a.Snapshot(ctx)
	if err != nil || !ok {
		t.Fatalf("reopen of a reachable recovered-with-health record: ok=%v err=%v", ok, err)
	}
	assertSameSnapshot(t, resumed, recovered)
	if n := recordCount(t, s); n != 1 {
		t.Fatalf("reopen wrote records: %d", n)
	}
}

// TestAdapterActiveWorkSurvivesDeferResumeReopen: active work is cleared
// only by the idle callback, so it survives defer into deferred AND resume
// back into ready. Both persisted records are snapshots the pure model can
// actually produce; the restore gate must accept each across a reopen.
func TestAdapterActiveWorkSurvivesDeferResumeReopen(t *testing.T) {
	root := t.TempDir()
	s, a := openAdapter(t, root)
	mustStart(t, a, "1.0.0")
	for _, event := range []operation.Event{operation.EventReady, operation.EventDownload, operation.EventDownloaded, operation.EventVerified, operation.EventWorkActive} {
		mustApply(t, a, operation.Input{Event: event})
	}
	deferred := mustApply(t, a, operation.Input{Event: operation.EventDefer})
	if deferred.State != operation.StateDeferred || !deferred.ActiveWork {
		t.Fatalf("invalid setup: %+v", deferred)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, a = openAdapter(t, root)
	assertSameSnapshot(t, currentSnapshot(t, a), deferred)
	resumed := mustApply(t, a, operation.Input{Event: operation.EventResume})
	if resumed.State != operation.StateReady || !resumed.ActiveWork {
		t.Fatalf("active work lost on resume: %+v", resumed)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_, a = openAdapter(t, root)
	assertSameSnapshot(t, currentSnapshot(t, a), resumed)
}

func assertSameSnapshot(t *testing.T, got, want operation.Snapshot) {
	t.Helper()
	if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatal("timestamp instant changed across persistence")
	}
	want.CreatedAt, want.UpdatedAt = got.CreatedAt, got.UpdatedAt
	if got != want {
		t.Fatalf("persisted snapshot changed: got %+v want %+v", got, want)
	}
}

// racing the SAME event against the SAME operation are serialized by the
// write transaction: exactly one event commits, the other is refused by the
// pure model against the post-commit state, and the persisted revision
// advanced exactly once.
func TestAdapterConcurrentApplySameIDSerializedAcrossHandles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s1, a1 := openAdapter(t, root)
	defer s1.Close()
	s2, a2 := openAdapter(t, root)
	defer s2.Close()

	started := mustStart(t, a1, "1.0.0")
	// The SAME event on both handles: announce is legal only from checking,
	// so no interleaving can legally commit twice — exactly one goroutine
	// commits and the loser is refused against the post-commit state.
	in1 := operation.Input{Event: operation.EventAnnounce, OperationID: started.ID}
	in2 := operation.Input{Event: operation.EventAnnounce, OperationID: started.ID}

	var wg sync.WaitGroup
	results := make(chan operation.State, 2)
	refusals := make(chan error, 2)
	adapters := []*Adapter{a1, a2}
	inputs := []operation.Input{in1, in2}
	for i := range inputs {
		wg.Add(1)
		go func(a *Adapter, in operation.Input) {
			defer wg.Done()
			snap, err := a.Apply(ctx, in)
			if err != nil {
				refusals <- err
				return
			}
			results <- snap.State
		}(adapters[i], inputs[i])
	}
	wg.Wait()
	close(results)
	close(refusals)

	var won operation.State
	committed := 0
	for state := range results {
		committed++
		won = state
	}
	if committed != 1 {
		t.Fatalf("serialized events committed %d times", committed)
	}
	if len(refusals) != 1 {
		t.Fatalf("serialized events produced %d refusals", len(refusals))
	}
	var opErr *operation.Error
	if err := <-refusals; !errors.As(err, &opErr) {
		t.Fatalf("loser refusal is %T: %v", err, err)
	}
	current := currentSnapshot(t, a1)
	if current.ID != started.ID || current.State != won {
		t.Fatalf("final state %s does not match the winning event %s", current.State, won)
	}
	_, revision, err := s1.LoadOperationRecord(ctx, started.ID)
	if err != nil || revision != 2 {
		t.Fatalf("revision after race: %d %v (want exactly one bump)", revision, err)
	}
}
