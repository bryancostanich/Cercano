package credentials

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/anthropicauth"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/secrets"
)

// admissionCounter is a fake in-memory work admission gate: acquire grants a
// lease by counting up, release counts down, and optional refusal/nil-release
// modes let tests exercise the failure paths. It never touches the network,
// the store, or any credential material — the hooks receive no profile names,
// tokens, or secret values at all.
type admissionCounter struct {
	active     atomic.Int32
	acquires   atomic.Int32
	releases   atomic.Int32
	refuse     error
	nilRelease bool
}

func (c *admissionCounter) acquire() (func(), error) {
	c.acquires.Add(1)
	if c.refuse != nil {
		return nil, c.refuse
	}
	if c.nilRelease {
		return nil, nil
	}
	c.active.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() {
			c.active.Add(-1)
			c.releases.Add(1)
		})
	}, nil
}

func awaitActive(t *testing.T, c *admissionCounter, want int32) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if c.active.Load() == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("admission active = %d, want %d", c.active.Load(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func awaitStoreValue(t *testing.T, st secrets.Store, name, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, _ := st.Get(name)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("store value = %q, want %q", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func liveFlightCount(s *Service) int {
	s.activityMu.Lock()
	n := len(s.liveFlights)
	s.activityMu.Unlock()
	return n
}

// TestUpdateCredentialBaselineWaiterReturnsBeforeRefreshCommits reproduces, on
// its own, the baseline behavior that makes flight-level tracking required: a
// waiter returns on ITS OWN cancellation while the shared refresh keeps
// running detached (its work context is WithoutCancel) and commits a rotated
// token AFTER the waiter returned. No admission hook is installed here, and no
// existing acquire/API wait behavior is altered — a gate that only counted
// waiters would report idle while this refresh was still running and writing.
func TestUpdateCredentialBaselineWaiterReturnsBeforeRefreshCommits(t *testing.T) {
	raw := secrets.NewMemory()
	raw.Set("work", "before")
	s := New(raw)
	ctx := testContext(t)
	waiterCtx, cancel := context.WithCancel(ctx)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := s.acquire(waiterCtx, "work", "anthropic", func(_ context.Context, tr *transaction) (token, error) {
			close(started)
			// Deliberately ignore cancellation: the resolver keeps running.
			<-release
			tr.Set("work", "after")
			return token{access: "after"}, nil
		})
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter error = %v, want context.Canceled", err)
	}
	if stored, _ := raw.Get("work"); stored != "before" {
		t.Fatalf("store mutated before the refresh returned: %q", stored)
	}
	// The waiter has returned; the refresh has not. It commits the rotated
	// token afterwards — work invisible to any waiter-counting gate.
	close(release)
	awaitStoreValue(t, raw, "work", "after")
}

// TestUpdateCredentialLastWaiterCancelKeepsLeaseUntilResolverExits proves the
// lease belongs to the refresh flight, not to the waiters: the last (and only)
// waiter returns on its cancellation, but the admitted lifetime persists until
// the resolver actually exits.
func TestUpdateCredentialLastWaiterCancelKeepsLeaseUntilResolverExits(t *testing.T) {
	s := New(secrets.NewMemory())
	counter := &admissionCounter{}
	if err := s.BindWorkAdmission(counter.acquire); err != nil {
		t.Fatal(err)
	}
	ctx := testContext(t)
	waiterCtx, cancel := context.WithCancel(ctx)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	done := make(chan error, 1)
	go func() {
		_, err := s.acquire(waiterCtx, "work", "anthropic", func(context.Context, *transaction) (token, error) {
			close(started)
			// Deliberately ignore cancellation: the flight has not exited.
			<-release
			return token{}, context.Canceled
		})
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter error = %v, want context.Canceled", err)
	}
	// The waiter has returned but the refresh has not: exactly one lease is
	// still held and the flight is still tracked.
	if n := counter.active.Load(); n != 1 {
		t.Fatalf("active leases after waiter return = %d, want 1", n)
	}
	if n := liveFlightCount(s); n != 1 {
		t.Fatalf("live flights after waiter return = %d, want 1", n)
	}
	// The resolver's actual exit — not the waiter's — retires the lease.
	unblock()
	awaitActive(t, counter, 0)
	deadline := time.Now().Add(3 * time.Second)
	for liveFlightCount(s) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("flight still tracked after resolver exit")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestUpdateCredentialSharedWaitersHoldOneLease proves a shared flight grants
// exactly one admitted lifetime no matter how many waiters join it.
func TestUpdateCredentialSharedWaitersHoldOneLease(t *testing.T) {
	s := New(secrets.NewMemory())
	counter := &admissionCounter{}
	if err := s.BindWorkAdmission(counter.acquire); err != nil {
		t.Fatal(err)
	}
	ctx := testContext(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	resolve := func(context.Context, *transaction) (token, error) {
		once.Do(func() { close(started) })
		<-release
		return token{access: "fresh"}, nil
	}
	const waiters = 3
	done := make(chan error, waiters)
	for i := 0; i < waiters; i++ {
		go func() {
			value, err := s.acquire(ctx, "work", "anthropic", resolve)
			if err == nil && value.access != "fresh" {
				err = errors.New("wrong token")
			}
			done <- err
		}()
	}
	<-started
	awaitWaiters(t, s, "work", waiters)
	if n := counter.active.Load(); n != 1 {
		t.Fatalf("shared flight holds %d leases, want 1", n)
	}
	if n := counter.acquires.Load(); n != 2 {
		t.Fatalf("shared flight made %d acquires, want 2 (bind probe + one flight)", n)
	}
	close(release)
	for i := 0; i < waiters; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	awaitActive(t, counter, 0)
	if n := counter.releases.Load(); n != 2 {
		t.Fatalf("releases = %d, want 2 (bind probe + one flight)", n)
	}
}

// TestUpdateCredentialSupersededFlightRetainsLeaseUntilReturn proves a
// superseded flight keeps its lease for its whole real lifetime even after
// profileState.flight has moved on: the gate must still see the stale refresh
// (and its possible rotated-token write attempt) until the goroutine returns.
func TestUpdateCredentialSupersededFlightRetainsLeaseUntilReturn(t *testing.T) {
	s := New(secrets.NewMemory())
	counter := &admissionCounter{}
	if err := s.BindWorkAdmission(counter.acquire); err != nil {
		t.Fatal(err)
	}
	ctx := testContext(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	resolve := func(_ context.Context, tr *transaction) (token, error) {
		if calls.Add(1) == 1 {
			close(started)
			// Deliberately ignore cancellation: superseded, but still running.
			<-release
			stale, _ := (anthropicauth.TokenSet{Access: "stale", ExpiresAt: time.Now().Add(time.Hour)}).Encode()
			tr.Set("work", stale)
			return token{access: "stale"}, nil
		}
		return token{access: "current"}, nil
	}
	result := make(chan token, 1)
	waitErr := make(chan error, 1)
	go func() {
		value, err := s.acquire(ctx, "work", "anthropic", resolve)
		result <- value
		waitErr <- err
	}()
	<-started
	p := s.profile("work")
	p.mu.Lock()
	old := p.flight
	p.mu.Unlock()
	if old == nil {
		t.Fatal("no flight to supersede")
	}
	// A login replacement supersedes the flight: it is canceled and its
	// waiters are woken, but the goroutine keeps running until release.
	fresh := "new-login"
	if err := s.Set("work", fresh); err != nil {
		t.Fatal(err)
	}
	// The superseded waiter retried on a fresh flight and got the live value.
	if err := <-waitErr; err != nil {
		t.Fatal(err)
	}
	if value := <-result; value.access != "current" {
		t.Fatalf("replacement blocked or lost: %q", value.access)
	}
	// The old flight's lease is retained even though p.flight moved on.
	awaitActive(t, counter, 1) // only the old flight's lease remains
	s.activityMu.Lock()
	_, retained := s.liveFlights[old]
	s.activityMu.Unlock()
	if !retained {
		t.Fatal("superseded flight dropped from tracking before its return")
	}
	// The old resolver's actual exit retires the last lease.
	close(release)
	<-old.exited
	awaitActive(t, counter, 0)
	if stored, _ := s.Get("work"); stored != fresh {
		t.Fatalf("superseded flight wrote stale data: %q", stored)
	}
}

// TestUpdateCredentialRefusedAdmissionStartsNoRefreshAndWritesNothing proves
// an admission refusal happens BEFORE the refresh starts: the resolver is
// never invoked (no network), the store is untouched (no secret mutation), no
// flight is created, and the returned error is the typed ErrAdmission —
// never a credential classification that callers could read as an invalid or
// revoked credential.
func TestUpdateCredentialRefusedAdmissionStartsNoRefreshAndWritesNothing(t *testing.T) {
	raw := secrets.NewMemory()
	raw.Set("work", "sentinel")
	s := New(raw)
	paused := errors.New("gate sealed")
	var hookCalls atomic.Int32
	// The hook grants the bind probe but refuses the flight itself, so the
	// refusal is exercised at refresh start, not at bind time.
	hook := func() (func(), error) {
		if hookCalls.Add(1) == 1 {
			return func() {}, nil // bind probe
		}
		return nil, paused
	}
	if err := s.BindWorkAdmission(hook); err != nil {
		t.Fatal(err)
	}
	var resolved atomic.Int32
	value, err := s.acquire(testContext(t), "work", "anthropic", func(context.Context, *transaction) (token, error) {
		resolved.Add(1)
		return token{access: "must-not-happen"}, nil
	})
	if value.access != "" {
		t.Fatalf("refused admission returned a token: %q", value.access)
	}
	if !errors.Is(err, ErrAdmission) {
		t.Fatalf("refusal error = %v, want ErrAdmission", err)
	}
	if !errors.Is(err, paused) {
		t.Fatalf("refusal does not wrap the gate's cause: %v", err)
	}
	if class := llm.ClassOf(err); class != llm.ErrUnknown {
		t.Fatalf("refusal classified as %q: must not look like an invalid or revoked credential", class)
	}
	if resolved.Load() != 0 {
		t.Fatal("refused admission started a refresh")
	}
	if stored, _ := raw.Get("work"); stored != "sentinel" {
		t.Fatalf("refused admission mutated the store: %q", stored)
	}
	p := s.profile("work")
	p.mu.Lock()
	f := p.flight
	p.mu.Unlock()
	if f != nil {
		t.Fatal("refused admission left a flight behind")
	}
	if n := liveFlightCount(s); n != 0 {
		t.Fatalf("refused admission left %d tracked flights", n)
	}
	if hookCalls.Load() != 2 { // bind probe + the refused flight acquire
		t.Fatalf("hook calls = %d, want 2", hookCalls.Load())
	}
}

// TestUpdateCredentialNilReleaseFromHookRefusesRefresh proves a hook that
// grants no release is treated as a refusal: the refresh never starts and the
// typed admission error is returned, with nothing written.
func TestUpdateCredentialNilReleaseFromHookRefusesRefresh(t *testing.T) {
	raw := secrets.NewMemory()
	raw.Set("work", "sentinel")
	s := New(raw)
	// The hook grants the bind probe but returns no release for the flight
	// itself, so the nil release is exercised at refresh start.
	var hookCalls atomic.Int32
	hook := func() (func(), error) {
		if hookCalls.Add(1) == 1 {
			return func() {}, nil // bind probe
		}
		return nil, nil // nil release for the flight
	}
	if err := s.BindWorkAdmission(hook); err != nil {
		t.Fatal(err)
	}
	var resolved atomic.Int32
	_, err := s.acquire(testContext(t), "work", "anthropic", func(context.Context, *transaction) (token, error) {
		resolved.Add(1)
		return token{access: "must-not-happen"}, nil
	})
	if !errors.Is(err, ErrAdmission) {
		t.Fatalf("nil-release error = %v, want ErrAdmission", err)
	}
	if resolved.Load() != 0 {
		t.Fatal("refresh started despite a nil release")
	}
	if stored, _ := raw.Get("work"); stored != "sentinel" {
		t.Fatalf("nil release mutated the store: %q", stored)
	}
}

// TestUpdateCredentialBindRejectsNilAndRebind proves nil acquire is rejected,
// a configured hook cannot be rebind, and the surviving hook still governs
// exactly one lease per flight.
func TestUpdateCredentialBindRejectsNilAndRebind(t *testing.T) {
	s := New(secrets.NewMemory())
	if err := s.BindWorkAdmission(nil); err == nil {
		t.Fatal("nil acquire accepted")
	}
	counter := &admissionCounter{}
	if err := s.BindWorkAdmission(counter.acquire); err != nil {
		t.Fatal(err)
	}
	if err := s.BindWorkAdmission(counter.acquire); err == nil {
		t.Fatal("rebind accepted")
	}
	if err := s.BindWorkAdmission(nil); err == nil {
		t.Fatal("nil acquire accepted after a successful bind")
	}
	// The originally configured hook is still in charge.
	ctx := testContext(t)
	done := make(chan error, 1)
	go func() {
		_, err := s.acquire(ctx, "work", "anthropic", func(context.Context, *transaction) (token, error) {
			return token{access: "fresh"}, nil
		})
		done <- err
	}()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := counter.acquires.Load(); n != 2 {
		t.Fatalf("acquires = %d, want 2 (bind probe + one flight)", n)
	}
	awaitActive(t, counter, 0)
}

// TestUpdateCredentialBindNilReleaseLeavesHookUnconfigured proves a bind whose
// hook returns no release fails and leaves the service in the pre-bind state:
// a later bind can still succeed.
func TestUpdateCredentialBindNilReleaseLeavesHookUnconfigured(t *testing.T) {
	s := New(secrets.NewMemory())
	if err := s.BindWorkAdmission((&admissionCounter{nilRelease: true}).acquire); err == nil {
		t.Fatal("bind with a nil-releasing hook accepted")
	}
	s.activityMu.Lock()
	bound := s.admission != nil
	s.activityMu.Unlock()
	if bound {
		t.Fatal("failed bind left the hook configured")
	}
	counter := &admissionCounter{}
	if err := s.BindWorkAdmission(counter.acquire); err != nil {
		t.Fatalf("bind after failed bind: %v", err)
	}
}

// TestUpdateCredentialLateBindLeasesActiveFlights proves binding retroactively
// attaches already-running flights: two live refreshes started before the bind
// each obtain a lease from it, without the bind reading any token value.
func TestUpdateCredentialLateBindLeasesActiveFlights(t *testing.T) {
	s := New(secrets.NewMemory())
	ctx := testContext(t)
	release := make(chan struct{})
	resolve := func(context.Context, *transaction) (token, error) {
		<-release
		return token{access: "fresh"}, nil
	}
	for _, name := range []string{"a", "b"} {
		go s.acquire(ctx, name, "anthropic", resolve)
	}
	awaitWaiters(t, s, "a", 1)
	awaitWaiters(t, s, "b", 1)
	counter := &admissionCounter{}
	if err := s.BindWorkAdmission(counter.acquire); err != nil {
		t.Fatal(err)
	}
	awaitActive(t, counter, 2) // both still-live flights retroactively leased
	if n := liveFlightCount(s); n != 2 {
		t.Fatalf("live flights = %d, want 2", n)
	}
	close(release)
	awaitActive(t, counter, 0)
}

// TestUpdateCredentialFailedLateBindUndoesLeasesAndLeavesHookUnconfigured
// proves a late bind that fails partway rolls back every lease it granted
// during the attempt and leaves the hook unconfigured: the still-live flights
// remain untracked rather than half-tracked, and a subsequent bind can
// retroactively lease them again.
func TestUpdateCredentialFailedLateBindUndoesLeasesAndLeavesHookUnconfigured(t *testing.T) {
	s := New(secrets.NewMemory())
	ctx := testContext(t)
	release := make(chan struct{})
	resolve := func(context.Context, *transaction) (token, error) {
		<-release
		return token{access: "fresh"}, nil
	}
	for _, name := range []string{"a", "b"} {
		go s.acquire(ctx, name, "anthropic", resolve)
	}
	awaitWaiters(t, s, "a", 1)
	awaitWaiters(t, s, "b", 1)
	refused := errors.New("gate sealed mid-bind")
	var granted atomic.Int32
	failing := func() (func(), error) {
		if granted.Load() == 1 { // the second retroactive lease fails
			return nil, refused
		}
		granted.Add(1)
		var once sync.Once
		return func() { once.Do(func() { granted.Add(-1) }) }, nil
	}
	err := s.BindWorkAdmission(failing)
	if err == nil {
		t.Fatal("failing late bind accepted")
	}
	if !errors.Is(err, refused) {
		t.Fatalf("bind error = %v, want the gate's refusal", err)
	}
	if n := granted.Load(); n != 0 {
		t.Fatalf("leases taken during the failed bind = %d, want 0 (all undone)", n)
	}
	s.activityMu.Lock()
	bound := s.admission != nil
	s.activityMu.Unlock()
	if bound {
		t.Fatal("failed bind left the hook configured")
	}
	// The still-live flights remain untracked, not half-tracked: a retry bind
	// can retroactively lease them.
	counter := &admissionCounter{}
	if err := s.BindWorkAdmission(counter.acquire); err != nil {
		t.Fatalf("retry bind: %v", err)
	}
	awaitActive(t, counter, 2)
	close(release)
	awaitActive(t, counter, 0)
}
