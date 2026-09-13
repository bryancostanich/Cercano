package worker

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/telemetry"
	"cercano/source/server/internal/usage"
	wire "cercano/source/server/pkg/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type immediateAccountingReceiver struct {
	mu           sync.Mutex
	observations []usage.AttemptObservation
	health       telemetry.AccountingHealth
	incomplete   bool
}

func immediateReceipt() <-chan bool { ch := make(chan bool, 1); ch <- true; close(ch); return ch }
func (r *immediateAccountingReceiver) TryAttemptBatch(a []usage.AttemptObservation) (<-chan bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observations = append(r.observations, a...)
	return immediateReceipt(), nil
}
func (r *immediateAccountingReceiver) TryRemoteAccountingHealth(_ string, h telemetry.AccountingHealth) (<-chan bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h.Sequence >= r.health.Sequence {
		r.health = h
	}
	return immediateReceipt(), nil
}
func (r *immediateAccountingReceiver) MarkAccountingIncomplete(string) {
	r.mu.Lock()
	r.incomplete = true
	r.mu.Unlock()
}

func TestWorkerAccountingDrainStopsProducersAndReportsFinalHealth(t *testing.T) {
	worker, client := accountingTestConnection(t)
	sink := &immediateAccountingReceiver{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ready, drain, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- receiveAccountingControlled(ctx, client, sink, "process", func() { close(ready) }, drain)
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("receiver not ready")
	}
	turnCtx, release, err := worker.beginAccountingTurn(t.Context(), &wire.AccountingWork{OperationId: "parent-operation", SessionId: "parent-session", Source: "main"}, "conversation")
	if err != nil {
		t.Fatal(err)
	}
	attempt := usage.StartAttempt(turnCtx, "fake", "fake")
	producerDone := make(chan struct{})
	go func() { <-turnCtx.Done(); attempt.Finish(usage.Interrupted); release(); close(producerDone) }()
	close(drain)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("drain did not finish")
	}
	<-producerDone
	if _, _, err = worker.beginAccountingTurn(t.Context(), &wire.AccountingWork{}, "late"); status.Code(err) != codes.Unavailable {
		t.Fatalf("new work after drain: %v", err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if !sink.health.Closed || sink.health.Pending != 0 || sink.health.Uncertain != 0 || sink.incomplete {
		t.Fatalf("final health=%+v incomplete=%v", sink.health, sink.incomplete)
	}
	final := sink.observations[len(sink.observations)-1]
	if final.Outcome != usage.Interrupted || final.Attribution.OperationID != "parent-operation" || final.Attribution.SessionID != "parent-session" || final.Attribution.ConversationID != "conversation" {
		t.Fatalf("producer final observation=%+v", final)
	}
}

func TestInvalidWorkerAttributionWarnsWithoutFailingWork(t *testing.T) {
	worker, client := accountingTestConnection(t)
	sink := &immediateAccountingReceiver{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ready, drain, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- receiveAccountingControlled(ctx, client, sink, "process", func() { close(ready) }, drain)
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("receiver not ready")
	}
	scope, release, err := worker.beginAccountingTurn(t.Context(), &wire.AccountingWork{Source: strings.Repeat("x", 1025)}, "conversation")
	if err != nil {
		t.Fatal(err)
	}
	usage.StartAttempt(scope, "fake", "fake").Finish(usage.Completed)
	release()
	close(drain)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("drain did not finish")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if !sink.incomplete || !sink.health.CoverageIncomplete || len(sink.observations) == 0 {
		t.Fatalf("invalid metadata failed work or lost warning: %+v", sink.health)
	}
}
