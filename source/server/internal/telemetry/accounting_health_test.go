package telemetry

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestRemoteHealthPersistsWithoutInventingAttempts(t *testing.T) {
	s := accountingTestStore(t)
	c := NewAccountingCollector(s, testAccountingOptions())
	newer := AccountingHealth{Sequence: 2, Lost: 7, CoverageIncomplete: true, LastError: "worker queue overflow"}
	receipt, err := c.TryRemoteHealth("worker/process/writer", newer)
	if err != nil {
		t.Fatal(err)
	}
	older, err := c.TryRemoteHealth("worker/process/writer", AccountingHealth{Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	drainAccounting(t, c)
	if !<-receipt || !<-older {
		t.Fatal("health records not acknowledged")
	}
	var raw string
	if err = s.db.QueryRow(`SELECT snapshot FROM accounting_health WHERE writer_id='worker/process/writer'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var persisted AccountingHealth
	if err = json.Unmarshal([]byte(raw), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Sequence != 2 || persisted.Lost != 7 || !persisted.CoverageIncomplete {
		t.Fatalf("stale status replaced newer: %+v", persisted)
	}
	var attempts int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM inference_attempts`).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("health fabricated attempts: %d %v", attempts, err)
	}
	// Host health counters measure admitted records, not unique inference calls.
	if h := c.Health(); h.Accepted != 2 || h.Persisted != 2 {
		t.Fatalf("health record accounting=%+v", h)
	}
}
func TestRemoteHealthBackpressureRemainsSenderOwned(t *testing.T) {
	store := &receiptStore{writes: make(chan receiptWrite)}
	c := NewAccountingCollector(store, testAccountingOptions())
	c.Emit(accountingFixture("one"))
	first := nextReceiptWrite(t, store)
	c.Emit(accountingFixture("two"))
	if _, err := c.TryRemoteHealth("worker", AccountingHealth{Sequence: 1}); !errors.Is(err, ErrAccountingCapacity) {
		t.Fatalf("admission=%v", err)
	}
	if h := c.Health(); h.Lost != 0 || h.Accepted != 2 {
		t.Fatalf("rejected remote health claimed loss: %+v", h)
	}
	close(first.release)
	second := nextReceiptWrite(t, store)
	close(second.release)
	drainAccounting(t, c)
}
func TestRemoteHealthValidation(t *testing.T) {
	for _, h := range []AccountingHealth{{}, {Sequence: 1, Pending: -1}, {Sequence: 1, Pending: 4097}, {Sequence: 1, Persisted: 1}, {Sequence: ^uint64(0)}} {
		if err := ValidateRemoteHealth("worker", h); err == nil {
			t.Fatalf("invalid health accepted: %+v", h)
		}
	}
	if err := ValidateRemoteHealth("worker", AccountingHealth{Sequence: 1, Accepted: 2, Persisted: 1, Lost: 99}); err != nil {
		t.Fatal(err)
	}
}
