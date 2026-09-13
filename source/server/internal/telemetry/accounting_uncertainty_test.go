package telemetry

import (
	"context"
	"testing"
)

func TestShutdownPreservesEarlierUncertainty(t *testing.T) {
	store := &receiptStore{writes: make(chan receiptWrite)}
	c := NewAccountingCollector(store, testAccountingOptions())
	// Synthetic prior state: two accepted observations exhausted retries before
	// this final outstanding observation. Shutdown must not erase that history.
	c.mu.Lock()
	c.health.Accepted = 2
	c.health.Uncertain = 2
	c.mu.Unlock()
	c.Emit(accountingFixture("last"))
	_ = nextReceiptWrite(t, store)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_ = c.Close(ctx)
	if got := c.Health().Uncertain; got != 3 {
		t.Fatalf("prior uncertainty overwritten: got %d, want 2 prior + 1 outstanding", got)
	}
	_ = c.Close(ctx)
	if got := c.Health().Uncertain; got != 3 {
		t.Fatalf("repeated close changed uncertainty: %d", got)
	}
}
