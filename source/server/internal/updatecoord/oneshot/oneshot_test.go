package oneshot

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/updatecoord/exclusion"
	"cercano/source/server/internal/updatecoord/operation"
	"cercano/source/server/internal/updatecoord/state"
)

// One-shot executor tests run ONLY against temporary state roots and the
// pure model + exclusion primitives. They spawn no processes, touch no real
// installation, and use no network.

const testInstallID = "test-install"

func openStore(t *testing.T) (*state.Store, *state.Adapter) {
	t.Helper()
	s, err := state.Open(t.TempDir(), testInstallID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, state.NewAdapter(s)
}

func mustStart(t *testing.T, a *state.Adapter, target string) operation.Snapshot {
	t.Helper()
	snap, err := a.Start(context.Background(), target)
	if err != nil {
		t.Fatalf("adapter Start(%q): %v", target, err)
	}
	return snap
}

func mustApply(t *testing.T, a *state.Adapter, in operation.Input) operation.Snapshot {
	t.Helper()
	if in.OperationID == 0 {
		snap, ok, err := a.Snapshot(context.Background())
		if err != nil || !ok {
			t.Fatalf("adapter Snapshot: ok=%v err=%v", ok, err)
		}
		in.OperationID = snap.ID
	}
	snap, err := a.Apply(context.Background(), in)
	if err != nil {
		t.Fatalf("adapter Apply(%s): %v", in.Event, err)
	}
	return snap
}

// driveToInstalling walks the pre-activation lifecycle through the adapter.
func driveToInstalling(t *testing.T, a *state.Adapter, target string) operation.Snapshot {
	t.Helper()
	mustStart(t, a, target)
	mustApply(t, a, operation.Input{Event: operation.EventReady})
	mustApply(t, a, operation.Input{Event: operation.EventDownload})
	mustApply(t, a, operation.Input{Event: operation.EventDownloaded})
	mustApply(t, a, operation.Input{Event: operation.EventVerified})
	mustApply(t, a, operation.Input{Event: operation.EventDrain})
	return mustApply(t, a, operation.Input{Event: operation.EventDrained})
}

func currentSnapshot(t *testing.T, a *state.Adapter) operation.Snapshot {
	t.Helper()
	snap, ok, err := a.Snapshot(context.Background())
	if err != nil || !ok {
		t.Fatalf("adapter Snapshot: ok=%v err=%v", ok, err)
	}
	return snap
}

// countingBackend returns a backend that records invocations and runs fn.
func countingBackend(calls *int32, fn func(ctx context.Context, c *Controller) error) Backend {
	return func(ctx context.Context, c *Controller) error {
		atomic.AddInt32(calls, 1)
		return fn(ctx, c)
	}
}

func TestRequestMismatchStaleTerminalDeferredRefuseWithoutCallback(t *testing.T) {
	for _, tc := range []struct {
		name string
		want error
		set  func(t *testing.T, a *state.Adapter) operation.Snapshot
		req  func(snap operation.Snapshot) Request
	}{
		{
			name: "wrong installation",
			want: ErrInstallMismatch,
			set:  func(t *testing.T, a *state.Adapter) operation.Snapshot { return mustStart(t, a, "1.0.0") },
			req: func(snap operation.Snapshot) Request {
				return Request{InstallID: "other-install", OperationID: snap.ID}
			},
		},
		{
			name: "zero operation id",
			want: ErrInvalidRequest,
			set:  func(t *testing.T, a *state.Adapter) operation.Snapshot { return mustStart(t, a, "1.0.0") },
			req: func(snap operation.Snapshot) Request {
				return Request{InstallID: testInstallID, OperationID: 0}
			},
		},
		{
			name: "stale operation id",
			want: ErrStaleOperation,
			set:  func(t *testing.T, a *state.Adapter) operation.Snapshot { return mustStart(t, a, "1.0.0") },
			req: func(snap operation.Snapshot) Request {
				return Request{InstallID: testInstallID, OperationID: snap.ID + 100}
			},
		},
		{
			name: "terminal cancelled operation",
			want: ErrTerminalOperation,
			set: func(t *testing.T, a *state.Adapter) operation.Snapshot {
				mustStart(t, a, "1.0.0")
				return mustApply(t, a, operation.Input{Event: operation.EventCancel})
			},
			req: func(snap operation.Snapshot) Request {
				return Request{InstallID: testInstallID, OperationID: snap.ID}
			},
		},
		{
			name: "deferred operation",
			want: ErrDeferredOperation,
			set: func(t *testing.T, a *state.Adapter) operation.Snapshot {
				mustStart(t, a, "1.0.0")
				return mustApply(t, a, operation.Input{Event: operation.EventDefer})
			},
			req: func(snap operation.Snapshot) Request {
				return Request{InstallID: testInstallID, OperationID: snap.ID}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, a := openStore(t)
			snap := tc.set(t, a)
			var calls int32
			ex, err := New(s, countingBackend(&calls, func(ctx context.Context, c *Controller) error {
				t.Error("backend invoked for a refused request")
				return nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			_, err = ex.Run(context.Background(), tc.req(snap))
			if !errors.Is(err, tc.want) {
				t.Fatalf("refusal: got %v, want %v", err, tc.want)
			}
			if n := atomic.LoadInt32(&calls); n != 0 {
				t.Fatalf("backend invoked %d times", n)
			}
			// Refusals leave the persisted state untouched.
			if snap.ID != 0 {
				after := currentSnapshot(t, a)
				if after.ID != snap.ID || after.State != snap.State {
					t.Fatalf("refused run mutated state: %+v", after)
				}
			}
		})
	}

	// No current operation at all: stale refusal, no callback.
	s, _ := openStore(t)
	var calls int32
	ex, err := New(s, countingBackend(&calls, func(ctx context.Context, c *Controller) error {
		t.Error("backend invoked with no current operation")
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ex.Run(context.Background(), Request{InstallID: testInstallID, OperationID: 1}); !errors.Is(err, ErrStaleOperation) {
		t.Fatalf("empty store: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Fatalf("backend invoked %d times", n)
	}
}

// TestSharedLaunchLockPreventsCallback: an outstanding shared LAUNCH lock
// (and an outstanding exclusive UPDATE lock) in the store's own directory
// keeps the one-shot utility from invoking any backend callback.
func TestSharedLaunchLockPreventsCallback(t *testing.T) {
	for _, mode := range []exclusion.Mode{exclusion.Launch, exclusion.Update} {
		t.Run(map[exclusion.Mode]string{exclusion.Launch: "launch", exclusion.Update: "update"}[mode], func(t *testing.T) {
			s, a := openStore(t)
			mustStart(t, a, "1.0.0")
			snap := currentSnapshot(t, a)

			held, err := exclusion.Acquire(context.Background(), s.Directory(), mode)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()

			var calls int32
			ex, err := New(s, countingBackend(&calls, func(ctx context.Context, c *Controller) error {
				t.Error("backend invoked while another lock is held")
				return nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			_, err = ex.Run(ctx, Request{InstallID: testInstallID, OperationID: snap.ID})
			if !errors.Is(err, ErrExclusionUnavailable) {
				t.Fatalf("blocked run: %v", err)
			}
			if n := atomic.LoadInt32(&calls); n != 0 {
				t.Fatalf("backend invoked %d times", n)
			}
			// Nothing advanced while the lock was unavailable.
			if after := currentSnapshot(t, a); after.State != operation.StateChecking {
				t.Fatalf("blocked run advanced state: %+v", after)
			}
		})
	}
}

// TestDuplicateExecutorsOnlyOneAdvances: two executors race for the same
// operation; the exclusive lease lets exactly one invoke the backend and
// the other refuses without any callback.
func TestDuplicateExecutorsOnlyOneAdvances(t *testing.T) {
	s, a := openStore(t)
	mustStart(t, a, "1.0.0")
	snap := currentSnapshot(t, a)

	invoked := make(chan *Controller, 4)
	release := make(chan struct{})
	var calls int32
	backend := countingBackend(&calls, func(ctx context.Context, c *Controller) error {
		invoked <- c
		<-release
		return errors.New("trusted backend fixture failure")
	})

	winner, err := New(s, backend)
	if err != nil {
		t.Fatal(err)
	}
	winnerDone := make(chan error, 1)
	go func() {
		_, err := winner.Run(context.Background(), Request{InstallID: testInstallID, OperationID: snap.ID})
		winnerDone <- err
	}()

	// Wait until the winner provably holds the lease inside its backend.
	var ctrl *Controller
	select {
	case ctrl = <-invoked:
	case <-time.After(5 * time.Second):
		t.Fatal("no executor acquired the lease")
	}

	// The duplicate executor is refused on the lease WITHOUT any callback.
	loser, err := New(s, countingBackend(&calls, func(ctx context.Context, c *Controller) error {
		t.Error("duplicate executor invoked the backend")
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if _, err := loser.Run(ctx, Request{InstallID: testInstallID, OperationID: snap.ID}); !errors.Is(err, ErrExclusionUnavailable) {
		t.Fatalf("duplicate run: %v", err)
	}

	close(release)
	winErr := <-winnerDone
	if !errors.Is(winErr, ErrBackendFailed) {
		t.Fatalf("winner run: %v", winErr)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("backend invoked %d times, want 1", n)
	}
	if ctrl == nil || ctrl.OperationID() != snap.ID {
		t.Fatal("winning controller was not bound to the requested operation")
	}
	// The winner's backend failure was durably recorded (generic failure).
	after := currentSnapshot(t, a)
	if after.State != operation.StateFailed || !after.HasFailure {
		t.Fatalf("winner's failure not recorded: %+v", after)
	}
}

// TestCallbackErrorLeavesDurableFailure: a backend error without its own
// recorded failure persists the sanitized generic failure, durably, and
// (being pre-activation) leaves retry free.
func TestCallbackErrorLeavesDurableFailure(t *testing.T) {
	root := t.TempDir()
	s, err := state.Open(root, testInstallID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := state.NewAdapter(s)
	mustStart(t, a, "1.0.0")
	snap := currentSnapshot(t, a)

	rawErr := errors.New("sensitive detail: /Users/me/secret.pem")
	ex, err := New(s, func(ctx context.Context, c *Controller) error {
		return rawErr
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ex.Run(context.Background(), Request{InstallID: testInstallID, OperationID: snap.ID})
	if !errors.Is(err, ErrBackendFailed) || !errors.Is(err, rawErr) {
		t.Fatalf("run error: %v", err)
	}

	after := currentSnapshot(t, a)
	if after.State != operation.StateFailed || !after.HasFailure {
		t.Fatalf("failure not recorded: %+v", after)
	}
	if after.Failure.Code != GenericFailureCode || after.Failure.UserReason != genericFailureReason {
		t.Fatalf("unsanitized failure persisted: %+v", after.Failure)
	}
	if after.RecoveryNeeded || after.RecoveryRequested {
		t.Fatalf("pre-activation failure must stay freely retryable: %+v", after)
	}

	// Durability: close and reopen, then retry stays free (pre-activation).
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = state.Open(root, testInstallID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a = state.NewAdapter(s)
	reopened := currentSnapshot(t, a)
	if reopened.State != operation.StateFailed || reopened.Failure.Code != GenericFailureCode {
		t.Fatalf("failure did not survive reopen: %+v", reopened)
	}
	next := mustStart(t, a, "1.1.0")
	if next.ID == snap.ID || next.State != operation.StateChecking {
		t.Fatalf("durable failure did not free a retry: %+v", next)
	}
}

// TestProtectedErrorCannotFakeCompletionOrRetry: a backend error inside the
// protected region records a recovery-needed failure: no retry, no re-run,
// and a nil-returning backend can never fake completion there.
func TestProtectedErrorCannotFakeCompletionOrRetry(t *testing.T) {
	s, a := openStore(t)
	snap := driveToInstalling(t, a, "2.0.0")

	ex, err := New(s, func(ctx context.Context, c *Controller) error {
		return errors.New("trusted backend fixture failure")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ex.Run(context.Background(), Request{InstallID: testInstallID, OperationID: snap.ID}); !errors.Is(err, ErrBackendFailed) {
		t.Fatalf("protected run: %v", err)
	}
	failed := currentSnapshot(t, a)
	if failed.State != operation.StateFailed || !failed.RecoveryNeeded {
		t.Fatalf("protected failure is not recovery-needed: %+v", failed)
	}
	if failed.Failure.Code != GenericFailureCode {
		t.Fatalf("generic failure not recorded in protected region: %+v", failed.Failure)
	}
	// Cannot retry: Start refuses until the backend resolves recovery.
	if _, err := a.Start(context.Background(), "2.1.0"); err == nil {
		t.Fatal("recovery-needed failure allowed a silent retry")
	} else {
		var opErr *operation.Error
		if !errors.As(err, &opErr) || opErr.Code != operation.MachineRecoveryNeeded {
			t.Fatalf("retry refusal: %v", err)
		}
	}
	// Cannot re-run the same operation: it is terminal.
	var calls int32
	ex2, err := New(s, countingBackend(&calls, func(ctx context.Context, c *Controller) error {
		t.Error("backend invoked on a terminal operation")
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ex2.Run(context.Background(), Request{InstallID: testInstallID, OperationID: snap.ID}); !errors.Is(err, ErrTerminalOperation) {
		t.Fatalf("terminal re-run: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Fatalf("backend invoked %d times", n)
	}

	// A nil-returning backend in a protected region cannot fake completion.
	s2, a2 := openStore(t)
	snap2 := driveToInstalling(t, a2, "3.0.0")
	exNil, err := New(s2, func(ctx context.Context, c *Controller) error {
		return nil // claims success without doing anything
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = exNil.Run(context.Background(), Request{InstallID: testInstallID, OperationID: snap2.ID}); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("nil-without-work run: %v", err)
	}
	after := currentSnapshot(t, a2)
	if after.State != operation.StateInstalling {
		t.Fatalf("incomplete run changed the protected state: %+v", after)
	}
}

// TestNilSuccessIncompleteRefusal: even a full happy-path backend whose
// final completion event is missing gets refused; success requires the
// PERSISTED StateComplete.
func TestNilSuccessIncompleteRefusal(t *testing.T) {
	s, a := openStore(t)
	mustStart(t, a, "1.0.0")
	snap := currentSnapshot(t, a)

	// Backend does real work, records progress, returns nil — but stops one
	// event short of completion.
	ex, err := New(s, func(ctx context.Context, c *Controller) error {
		for _, ev := range []operation.Event{operation.EventReady, operation.EventDownload, operation.EventDownloaded, operation.EventVerified} {
			if _, err := c.Apply(context.Background(), operation.Input{Event: ev}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ex.Run(context.Background(), Request{InstallID: testInstallID, OperationID: snap.ID}); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("incomplete run: %v", err)
	}
	if after := currentSnapshot(t, a); after.State != operation.StateWaiting {
		t.Fatalf("incomplete run mutated state: %+v", after)
	}

	// Completing one operation legitimately succeeds.
	exOK, err := New(s, func(ctx context.Context, c *Controller) error {
		for _, ev := range []operation.Event{
			operation.EventDrain, operation.EventDrained, operation.EventInstalled,
			operation.EventRestarted, operation.EventHealthSuccess, operation.EventCleanupDone,
		} {
			if _, err := c.Apply(context.Background(), operation.Input{Event: ev}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := exOK.Run(context.Background(), Request{InstallID: testInstallID, OperationID: snap.ID})
	if err != nil {
		t.Fatal(err)
	}
	if res.OperationID != snap.ID || res.State != operation.StateComplete || res.TargetVersion != "1.0.0" {
		t.Fatalf("success result: %+v", res)
	}
}

// TestBrokenProgressReporterDoesNotCancelJob: an erroring or panicking
// progress reporter never decides the transaction outcome; the operation
// still completes, and reports carry only safe fields.
func TestBrokenProgressReporterDoesNotCancelJob(t *testing.T) {
	for _, tc := range []struct {
		name   string
		broken func(p Progress) error
	}{
		{
			name:   "erroring reporter",
			broken: func(p Progress) error { return errors.New("broken writer") },
		},
		{
			name:   "panicking reporter",
			broken: func(p Progress) error { panic("catastrophic writer") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, a := openStore(t)
			mustStart(t, a, "1.0.0")
			snap := currentSnapshot(t, a)

			var mu sync.Mutex
			var seen []Progress
			ex, err := New(s, func(ctx context.Context, c *Controller) error {
				for _, ev := range []operation.Event{
					operation.EventReady, operation.EventDownload, operation.EventDownloaded, operation.EventVerified,
					operation.EventDrain, operation.EventDrained, operation.EventInstalled,
					operation.EventRestarted, operation.EventHealthSuccess, operation.EventCleanupDone,
				} {
					if _, err := c.Apply(ctx, operation.Input{Event: ev}); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			ex.SetProgressReporter(func(p Progress) error {
				mu.Lock()
				defer mu.Unlock()
				seen = append(seen, p)
				return tc.broken(p)
			})
			res, err := ex.Run(context.Background(), Request{InstallID: testInstallID, OperationID: snap.ID})
			if err != nil || res.State != operation.StateComplete {
				t.Fatalf("broken reporter decided the outcome: res=%+v err=%v", res, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(seen) == 0 {
				t.Fatal("reporter never invoked")
			}
			// Every delivered report carries only safe fields, correctly attributed.
			for _, p := range seen {
				if p.InstallID != testInstallID || p.OperationID != snap.ID || p.TargetVersion != "1.0.0" {
					t.Fatalf("unsafe or misattributed progress report: %+v", p)
				}
			}
		})
	}
}

// TestLockReleasedOnNormalAndErrorReturn: after every run — success, backend
// error, and refusal — the exclusive lease is free again in the store's own
// directory.
func TestLockReleasedOnNormalAndErrorReturn(t *testing.T) {
	s, a := openStore(t)
	mustStart(t, a, "1.0.0")
	snap := currentSnapshot(t, a)

	mustReacquire := func(t *testing.T, stage string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		h, err := exclusion.Acquire(ctx, s.Directory(), exclusion.Update)
		if err != nil {
			t.Fatalf("%s: lease not released: %v", stage, err)
		}
		if err := h.Close(); err != nil {
			t.Fatal(err)
		}
	}

	// Refusal path (wrong installation) releases.
	ex, err := New(s, func(ctx context.Context, c *Controller) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ex.Run(context.Background(), Request{InstallID: "other", OperationID: snap.ID}); !errors.Is(err, ErrInstallMismatch) {
		t.Fatalf("mismatch: %v", err)
	}
	mustReacquire(t, "after refusal")

	// Backend-error path releases.
	exErr, err := New(s, func(ctx context.Context, c *Controller) error {
		return errors.New("trusted backend fixture failure")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = exErr.Run(context.Background(), Request{InstallID: testInstallID, OperationID: snap.ID}); !errors.Is(err, ErrBackendFailed) {
		t.Fatalf("error run: %v", err)
	}
	mustReacquire(t, "after backend error")

	// The lease lives in the store's own directory.
	if got, err := filepath.Abs(s.Directory()); err != nil || !filepath.IsAbs(got) {
		t.Fatalf("store directory not a stable absolute path: %q (%v)", s.Directory(), err)
	}
}

// TestCallbackSeesHeldLock: while the trusted backend runs, the exclusive
// lease it holds in the store's directory blocks both shared launch and
// exclusive update acquisitions.
func TestCallbackSeesHeldLock(t *testing.T) {
	s, a := openStore(t)
	mustStart(t, a, "1.0.0")
	snap := currentSnapshot(t, a)

	ex, err := New(s, func(ctx context.Context, c *Controller) error {
		for _, mode := range []exclusion.Mode{exclusion.Update, exclusion.Launch} {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			h, err := exclusion.Acquire(ctx, s.Directory(), mode)
			cancel()
			if h != nil {
				_ = h.Close()
				t.Errorf("backend acquired mode %d while holding the lease", mode)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("backend lock probe for mode %d: %v", mode, err)
			}
		}
		return errors.New("trusted backend fixture failure")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ex.Run(context.Background(), Request{InstallID: testInstallID, OperationID: snap.ID}); !errors.Is(err, ErrBackendFailed) {
		t.Fatalf("held-lock run: %v", err)
	}
}

// TestCancelledUtilityContextStillRecordsOutcome: a context cancelled during
// (or by) the backend — the shape of a UI-disconnect or aborted utility —
// cannot fake success, cannot skip outcome recording, and cannot free
// admission for a protected interrupted state.
func TestCancelledUtilityContextStillRecordsOutcome(t *testing.T) {
	s, a := openStore(t)
	snap := driveToInstalling(t, a, "2.0.0")

	ctx, cancel := context.WithCancel(context.Background())
	ex, err := New(s, func(ctx context.Context, c *Controller) error {
		cancel() // the utility lifetime ends mid-work, protected region active
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ex.Run(ctx, Request{InstallID: testInstallID, OperationID: snap.ID})
	if !errors.Is(err, ErrBackendFailed) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run: %v", err)
	}
	cancel()

	after := currentSnapshot(t, a)
	if after.State != operation.StateFailed || !after.RecoveryNeeded {
		t.Fatalf("protected interrupted state not recorded recovery-needed: %+v", after)
	}
	if after.Failure.Code != GenericFailureCode {
		t.Fatalf("outcome not recorded with the sanitized generic failure: %+v", after.Failure)
	}
	// Admission is NOT released by the disconnect: retry stays blocked.
	if _, err := a.Start(context.Background(), "2.1.0"); err == nil {
		t.Fatal("UI disconnect released admission for a recovery-needed operation")
	}
}
