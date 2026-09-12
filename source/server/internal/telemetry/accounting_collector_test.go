package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/usage"
)

type gateAttemptStore struct {
	entered            chan struct{}
	release            chan struct{}
	once               sync.Once
	calls              atomic.Int64
	failFirst          bool
	ignoreCancellation bool
}

func (s *gateAttemptStore) InitializeAccounting(context.Context) error { return nil }
func (s *gateAttemptStore) WriteAttempts(ctx context.Context, _ []usage.AttemptObservation) error {
	n := s.calls.Add(1)
	if s.entered != nil {
		s.once.Do(func() { close(s.entered) })
	}
	if s.release != nil {
		if s.ignoreCancellation {
			<-s.release
		} else {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-s.release:
			}
		}
	}
	if s.failFirst && n == 1 {
		return errors.New("temporary storage error")
	}
	return nil
}
func testAccountingOptions() AccountingOptions {
	return AccountingOptions{Capacity: 2, BatchSize: 1, FlushInterval: time.Millisecond, WriteTimeout: time.Second, RetryDelay: time.Millisecond, MaxRetries: 1}
}
func drainAccounting(t *testing.T, c *AccountingCollector) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAccountingCollectorBoundedAdmission(t *testing.T) {
	store := &gateAttemptStore{entered: make(chan struct{}), release: make(chan struct{})}
	c := NewAccountingCollector(store, testAccountingOptions())
	if !c.Emit(accountingFixture("one")) {
		t.Fatal("first rejected")
	}
	<-store.entered
	admission := make(chan [2]bool, 1)
	go func() { admission <- [2]bool{c.Emit(accountingFixture("two")), c.Emit(accountingFixture("three"))} }()
	select {
	case got := <-admission:
		if !got[0] || got[1] {
			t.Fatalf("admission=%v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("admission blocked on storage")
	}
	h := c.Health()
	if h.Accepted != 2 || h.Pending != 2 || h.Lost != 1 || h.OldestPending.IsZero() || h.LastError == "" {
		t.Fatalf("saturation invisible: %+v", h)
	}
	close(store.release)
	drainAccounting(t, c)
	h = c.Health()
	if h.Persisted != 2 || h.Pending != 0 || h.Lost != 1 {
		t.Fatalf("drained=%+v", h)
	}
	if c.Emit(accountingFixture("late")) {
		t.Fatal("admitted after close")
	}
}
func TestAccountingCollectorRetry(t *testing.T) {
	store := &gateAttemptStore{failFirst: true}
	c := NewAccountingCollector(store, testAccountingOptions())
	c.Emit(accountingFixture("one"))
	drainAccounting(t, c)
	h := c.Health()
	if h.Persisted != 1 || h.Retries != 1 || h.WriteFailures != 1 || h.Uncertain != 0 || h.Pending != 0 {
		t.Fatalf("retry=%+v", h)
	}
}
func TestAccountingCollectorBoundedClose(t *testing.T) {
	store := &gateAttemptStore{entered: make(chan struct{}), release: make(chan struct{}), ignoreCancellation: true}
	c := NewAccountingCollector(store, testAccountingOptions())
	c.Emit(accountingFixture("one"))
	<-store.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("close=%v", err)
	}
	h := c.Health()
	if !h.Closed || h.Uncertain != 1 || h.Pending != 1 {
		t.Fatalf("shutdown uncertainty missing: %+v", h)
	}
	close(store.release)
	select {
	case <-c.done:
	case <-time.After(time.Second):
		t.Fatal("writer did not exit after storage release")
	}
}
func TestAccountingCollectorConcurrentAdmissionAndClose(t *testing.T) {
	c := NewAccountingCollector(&gateAttemptStore{}, testAccountingOptions())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				c.Emit(accountingFixture(usage.NewIdentity()))
				c.Health()
			}
		}()
	}
	drainAccounting(t, c)
	wg.Wait()
	h := c.Health()
	if h.Accepted+h.Lost != 400 || h.Pending != 0 || h.Accepted != h.Persisted {
		t.Fatalf("concurrent balance=%+v", h)
	}
}
func TestAccountingCollectorSQLiteHealth(t *testing.T) {
	s := accountingTestStore(t)
	c := NewAccountingCollector(s, testAccountingOptions())
	c.Emit(accountingFixture("one"))
	c.Emit(usage.AttemptObservation{}) // invalid loss visible independently
	drainAccounting(t, c)
	var raw string
	if err := s.db.QueryRow(`SELECT snapshot FROM accounting_health WHERE writer_id=?`, c.writerID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var h AccountingHealth
	if err := json.Unmarshal([]byte(raw), &h); err != nil {
		t.Fatal(err)
	}
	if h.Lost != 1 || h.Persisted != 1 || !h.Closed || h.Pending != 0 {
		t.Fatalf("persisted health=%+v", h)
	}
	got, err := s.AccountingAttempt(t.Context(), "one")
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != usage.Started || got.Tokens.TotalsKnown() {
		t.Fatalf("unfinished attempt fabricated completion: %+v", got)
	}
}
