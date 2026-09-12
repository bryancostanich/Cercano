package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"cercano/source/server/internal/usage"
)

type receiptWrite struct {
	release      chan struct{}
	observations []usage.AttemptObservation
}
type receiptStore struct{ writes chan receiptWrite }

func (s *receiptStore) InitializeAccounting(context.Context) error { return nil }
func (s *receiptStore) WriteAttempts(ctx context.Context, observations []usage.AttemptObservation) error {
	call := receiptWrite{release: make(chan struct{}), observations: observations}
	select {
	case s.writes <- call:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-call.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func nextReceiptWrite(t *testing.T, s *receiptStore) receiptWrite {
	t.Helper()
	select {
	case w := <-s.writes:
		return w
	case <-time.After(time.Second):
		t.Fatal("writer did not receive batch")
		return receiptWrite{}
	}
}
func expectNoReceipt(t *testing.T, receipt <-chan bool) {
	t.Helper()
	select {
	case v := <-receipt:
		t.Fatalf("premature persistence receipt: %v", v)
	default:
	}
}

func TestAccountingReceiptWaitsForEveryWrite(t *testing.T) {
	store := &receiptStore{writes: make(chan receiptWrite)}
	options := testAccountingOptions()
	options.Capacity = 4
	options.BatchSize = 1
	c := NewAccountingCollector(store, options)
	input := []usage.AttemptObservation{accountingFixture("one"), accountingFixture("two"), accountingFixture("three")}
	receipt, err := c.TryBatch(input)
	if err != nil {
		t.Fatal(err)
	}
	input[0].ID = "mutated-after-admission"
	for i := 0; i < 3; i++ {
		write := nextReceiptWrite(t, store)
		if i == 0 && write.observations[0].ID != "one" {
			t.Fatal("caller mutation reached queue")
		}
		expectNoReceipt(t, receipt)
		close(write.release)
	}
	select {
	case committed := <-receipt:
		if !committed {
			t.Fatal("successful writes not acknowledged")
		}
	case <-time.After(time.Second):
		t.Fatal("missing receipt")
	}
	drainAccounting(t, c)
	if h := c.Health(); h.Persisted != 3 || h.Pending != 0 {
		t.Fatalf("batch accounting=%+v", h)
	}
}
func TestAccountingBatchRejectionDoesNotClaimLoss(t *testing.T) {
	store := &receiptStore{writes: make(chan receiptWrite)}
	c := NewAccountingCollector(store, testAccountingOptions())
	receipt, err := c.TryBatch([]usage.AttemptObservation{accountingFixture("one"), accountingFixture("two")})
	if err != nil {
		t.Fatal(err)
	}
	first := nextReceiptWrite(t, store)
	if _, err = c.TryBatch([]usage.AttemptObservation{accountingFixture("retry-owned-by-worker")}); !errors.Is(err, ErrAccountingCapacity) {
		t.Fatalf("rejection=%v", err)
	}
	if _, err = c.TryBatch([]usage.AttemptObservation{accountingFixture("valid"), {}}); err == nil {
		t.Fatal("malformed batch admitted")
	}
	if h := c.Health(); h.Lost != 0 || h.Accepted != 2 || h.Pending != 2 {
		t.Fatalf("rejection claimed ownership/loss: %+v", h)
	}
	expectNoReceipt(t, receipt)
	close(first.release)
	second := nextReceiptWrite(t, store)
	close(second.release)
	drainAccounting(t, c)
	if !<-receipt {
		t.Fatal("initial batch failed")
	}
	if _, err = c.TryBatch([]usage.AttemptObservation{accountingFixture("late")}); !errors.Is(err, ErrAccountingClosed) {
		t.Fatalf("closed rejection=%v", err)
	}
}
func TestAccountingReceiptFailsOnShutdown(t *testing.T) {
	store := &receiptStore{writes: make(chan receiptWrite)}
	c := NewAccountingCollector(store, testAccountingOptions())
	receipt, err := c.TryBatch([]usage.AttemptObservation{accountingFixture("unacknowledged")})
	if err != nil {
		t.Fatal(err)
	}
	_ = nextReceiptWrite(t, store)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err = c.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("close=%v", err)
	}
	select {
	case committed := <-receipt:
		if committed {
			t.Fatal("uncommitted batch acknowledged")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown left a receipt unresolved")
	}
}

type receiptFailStore struct{}

func (*receiptFailStore) InitializeAccounting(context.Context) error { return nil }
func (*receiptFailStore) WriteAttempts(context.Context, []usage.AttemptObservation) error {
	return errors.New("storage failed")
}
func TestAccountingReceiptFailsAfterRetries(t *testing.T) {
	c := NewAccountingCollector(&receiptFailStore{}, testAccountingOptions())
	receipt, err := c.TryBatch([]usage.AttemptObservation{accountingFixture("unacknowledged")})
	if err != nil {
		t.Fatal(err)
	}
	drainAccounting(t, c)
	if <-receipt {
		t.Fatal("failed writes acknowledged")
	}
	if h := c.Health(); h.Uncertain != 1 || h.Persisted != 0 || h.Retries != 1 {
		t.Fatalf("failed writes=%+v", h)
	}
}
