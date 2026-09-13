package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"cercano/source/server/internal/telemetry"
	"cercano/source/server/internal/usage"
	wire "cercano/source/server/pkg/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "google.golang.org/protobuf/proto"
)

type accountingWait struct {
	id      string
	receipt chan *wire.WorkerAccountingReceipt
}
type accountingLink struct {
	send    chan *wire.WorkerAccountingBatch
	closed  chan struct{}
	waiting *accountingWait // guarded by writer.mu
}

// workerAccountingWriter is only a background persistence adapter. The shared
// telemetry collector owns bounded admission and retries; inference never calls
// WriteAttempts, serializes wire data, or waits here.
type workerAccountingWriter struct {
	statusSequence uint64
	mu             sync.Mutex
	link           *accountingLink
}

func (w *workerAccountingWriter) InitializeAccounting(context.Context) error { return nil }
func (w *workerAccountingWriter) attach() (*accountingLink, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.link != nil {
		return nil, status.Error(codes.AlreadyExists, "worker accounting already attached")
	}
	link := &accountingLink{send: make(chan *wire.WorkerAccountingBatch, 1), closed: make(chan struct{})}
	w.link = link
	return link, nil
}
func (w *workerAccountingWriter) detach(link *accountingLink) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.link == link {
		w.link = nil
		close(link.closed)
	}
}
func (w *workerAccountingWriter) deliver(link *accountingLink, r *wire.WorkerAccountingReceipt) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.link != link || link.waiting == nil || link.waiting.id != r.BatchId {
		return
	}
	select {
	case link.waiting.receipt <- r:
	default:
	} // duplicate acknowledgment
}
func (w *workerAccountingWriter) WriteAttempts(ctx context.Context, observations []usage.AttemptObservation) error {
	batch, err := accountingBatchToWire("pending", observations)
	if err != nil {
		return err
	}
	return w.writeBatch(ctx, batch)
}

func (w *workerAccountingWriter) WriteAccountingHealth(ctx context.Context, writer string, h telemetry.AccountingHealth) error {
	w.mu.Lock()
	w.statusSequence++
	h.Sequence = w.statusSequence
	w.mu.Unlock()
	batch, err := accountingHealthToWire("pending", writer, h)
	if err != nil {
		return err
	}
	return w.writeBatch(ctx, batch)
}

func (w *workerAccountingWriter) writeBatch(ctx context.Context, batch *wire.WorkerAccountingBatch) error {
	bytes, err := (pb.MarshalOptions{Deterministic: true}).Marshal(batch)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(bytes)
	batch.BatchId = hex.EncodeToString(hash[:])
	if pb.Size(batch) > maxAccountingWireBytes {
		return fmt.Errorf("accounting batch exceeds wire limit")
	}
	wait := &accountingWait{id: batch.BatchId, receipt: make(chan *wire.WorkerAccountingReceipt, 1)}
	w.mu.Lock()
	link := w.link
	if link == nil {
		w.mu.Unlock()
		return errors.New("worker accounting not connected")
	}
	if link.waiting != nil {
		w.mu.Unlock()
		return errors.New("concurrent accounting writes unsupported")
	}
	link.waiting = wait
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		if link.waiting == wait {
			link.waiting = nil
		}
		w.mu.Unlock()
	}()
	select {
	case link.send <- batch:
	case <-ctx.Done():
		return ctx.Err()
	case <-link.closed:
		return errors.New("worker accounting disconnected")
	}
	select {
	case r := <-wait.receipt:
		if r.Persisted {
			return nil
		}
		// Host error detail is diagnostic metadata, not an inference failure. Keep
		// errors generic here; the collector exposes retry/failure health separately.
		return errors.New("worker accounting persistence not acknowledged")
	case <-ctx.Done():
		return ctx.Err()
	case <-link.closed:
		return errors.New("worker accounting disconnected")
	}
}

func (w *WorkerServer) accountingWriter() *workerAccountingWriter {
	w.accountingMu.Lock()
	defer w.accountingMu.Unlock()
	if w.accountingTransport == nil {
		w.accountingTransport = &workerAccountingWriter{}
	}
	return w.accountingTransport
}

// Accounting is independent of RunTurn and carries no permission/control RPCs.
// The host owns its process-level stream context, not a user turn's context.
func (w *WorkerServer) Accounting(stream wire.Worker_AccountingServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if first.BatchId != "" || first.Persisted || first.Retryable || first.Error != "" || first.Drain {
		return status.Error(codes.InvalidArgument, "accounting stream must start with an empty receipt")
	}
	writer := w.accountingWriter()
	link, err := writer.attach()
	if err != nil {
		return err
	}
	defer writer.detach(link)
	if err := stream.Send(&wire.WorkerAccountingBatch{}); err != nil {
		return err
	}
	recvDone := make(chan error, 1)
	drainResult := make(chan *wire.WorkerAccountingBatch, 1)
	var drainOnce sync.Once
	go func() {
		for {
			receipt, err := stream.Recv()
			if err != nil {
				recvDone <- err
				return
			}
			if receipt.Drain {
				if receipt.BatchId != "" || receipt.Persisted || receipt.Retryable || receipt.Error != "" {
					recvDone <- status.Error(codes.InvalidArgument, "invalid accounting drain request")
					return
				}
				drainOnce.Do(func() { go func() { drainResult <- w.finishAccounting(stream.Context()) }() })
				continue
			}
			if receipt.BatchId == "" || len(receipt.BatchId) > 1024 || len(receipt.Error) > 1024 {
				recvDone <- status.Error(codes.InvalidArgument, "invalid accounting receipt")
				return
			}
			writer.deliver(link, receipt)
		}
	}()
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case err := <-recvDone:
			return err
		case result := <-drainResult:
			return stream.Send(result)
		case batch := <-link.send:
			if err := stream.Send(batch); err != nil {
				return err
			}
		}
	}
}
