package worker

import (
	"context"
	"errors"
	"sync"
	"time"

	"cercano/source/server/internal/telemetry"
	"cercano/source/server/internal/usage"
	wire "cercano/source/server/pkg/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (w *WorkerServer) beginAccountingTurn(parent context.Context, work *wire.AccountingWork, conversation string) (context.Context, func(), error) {
	w.accountingMu.Lock()
	if w.accountingClosing {
		w.accountingMu.Unlock()
		return nil, nil, status.Error(codes.Unavailable, "worker is shutting down")
	}
	if w.accountingTransport == nil {
		w.accountingTransport = &workerAccountingWriter{}
	}
	if w.accountingCollector == nil {
		w.accountingCollector = telemetry.NewAccountingCollector(w.accountingTransport, telemetry.AccountingOptions{})
	}
	if w.accountingTurns == nil {
		w.accountingTurns = make(map[string]context.CancelFunc)
	}
	collector := w.accountingCollector
	ctx, cancel := context.WithCancel(parent)
	registration := usage.NewIdentity()
	w.accountingTurns[registration] = cancel
	w.accountingWorkers.Add(1)
	w.accountingMu.Unlock()
	a := usage.Attribution{OperationID: work.GetOperationId(), ConversationID: work.GetConversationId(), SessionID: work.GetSessionId(), Source: work.GetSource()}
	if a.ConversationID == "" {
		a.ConversationID = conversation
	}
	if a.Source == "" {
		a.Source = "main"
	}
	// Bad accounting metadata must not fail inference. Preserve explicit unknown
	// attribution and a coverage warning rather than keeping oversized strings.
	for _, field := range []*string{&a.OperationID, &a.ConversationID, &a.SessionID, &a.Source} {
		if len(*field) > 1024 {
			*field = ""
			collector.MarkCoverageIncomplete("invalid worker accounting attribution")
		}
	}
	if a.OperationID == "" {
		a.OperationID = usage.NewIdentity()
	}
	ctx = usage.WithAttempts(ctx, collector.Emit, a)
	var once sync.Once
	release := func() {
		once.Do(func() {
			cancel()
			w.accountingMu.Lock()
			delete(w.accountingTurns, registration)
			w.accountingMu.Unlock()
			w.accountingWorkers.Done()
		})
	}
	return ctx, release, nil
}

func (w *WorkerServer) startAccountingClose() <-chan struct{} {
	w.accountingCloseOnce.Do(func() {
		w.accountingMu.Lock()
		w.accountingClosing = true
		w.accountingCloseDone = make(chan struct{})
		done := w.accountingCloseDone
		collector := w.accountingCollector
		cancels := make([]context.CancelFunc, 0, len(w.accountingTurns))
		for _, cancel := range w.accountingTurns {
			cancels = append(cancels, cancel)
		}
		w.accountingMu.Unlock()
		for _, cancel := range cancels {
			cancel()
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			producersDone := make(chan struct{})
			go func() { w.accountingWorkers.Wait(); close(producersDone) }()
			var err error
			select {
			case <-producersDone:
			case <-ctx.Done():
				err = ctx.Err()
				if collector != nil {
					collector.MarkCoverageIncomplete("worker producers did not stop")
				}
			}
			if collector != nil {
				err = errors.Join(err, collector.Close(ctx))
			}
			w.accountingMu.Lock()
			w.accountingCloseErr = err
			close(done)
			w.accountingMu.Unlock()
		}()
	})
	w.accountingMu.Lock()
	done := w.accountingCloseDone
	w.accountingMu.Unlock()
	return done
}

// finishAccounting is called only by the accounting RPC's drain request, never
// by normal turn completion. Reconnection may retry final health delivery after
// the one process-level close operation has completed.
func (w *WorkerServer) finishAccounting(ctx context.Context) *wire.WorkerAccountingBatch {
	out := &wire.WorkerAccountingBatch{DrainFinished: true}
	select {
	case <-w.startAccountingClose():
	case <-ctx.Done():
		out.DrainError = "worker accounting shutdown incomplete"
		return out
	}
	w.accountingMu.Lock()
	collector, closeErr := w.accountingCollector, w.accountingCloseErr
	w.accountingMu.Unlock()
	h := telemetry.AccountingHealth{Closed: true}
	id := "empty"
	if collector != nil {
		h = collector.Health()
		id = collector.WriterID()
	}
	finalCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	healthErr := w.accountingWriter().WriteAccountingHealth(finalCtx, id, h)
	if closeErr != nil || healthErr != nil {
		out.DrainError = "worker accounting shutdown incomplete"
	}
	return out
}

// CloseAccounting is for process shutdown, not turn completion. The caller's
// deadline bounds its wait; the one close operation has its own two-second cap.
func (w *WorkerServer) CloseAccounting(ctx context.Context) error {
	select {
	case <-w.startAccountingClose():
	case <-ctx.Done():
		return ctx.Err()
	}
	w.accountingMu.Lock()
	defer w.accountingMu.Unlock()
	return w.accountingCloseErr
}
