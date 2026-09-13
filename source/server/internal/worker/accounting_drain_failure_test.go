package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	wire "cercano/source/server/pkg/proto"
)

func TestWorkerAccountingDrainReportsUnstoppedProducer(t *testing.T) {
	worker, client := accountingTestConnection(t)
	sink := &immediateAccountingReceiver{}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ready, drain, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- receiveAccountingControlled(ctx, client, sink, "process", func() { close(ready) }, drain)
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("receiver not ready")
	}
	_, release, err := worker.beginAccountingTurn(t.Context(), &wire.AccountingWork{}, "conversation")
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately keep this synthetic producer registered after cancellation.
	// Release it after the bounded failure is observed so the test leaks nothing.
	defer release()
	close(drain)
	select {
	case err := <-done:
		if !errors.Is(err, errAccountingDrainIncomplete) {
			t.Fatalf("drain result=%v", err)
		}
	case <-ctx.Done():
		t.Fatal("drain exceeded its budget")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if !sink.incomplete {
		t.Fatal("unstopped producer was reported as complete")
	}
	if sink.health.Lost != 0 {
		t.Fatalf("invented a count of missing observations: %+v", sink.health)
	}
}
