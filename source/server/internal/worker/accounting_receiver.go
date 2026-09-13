package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"cercano/source/server/internal/telemetry"
	"cercano/source/server/internal/usage"
	wire "cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
)

// AccountingReceiver is the existing collector's background-admission surface.
// It neither dispatches tools nor participates in inference or permissions.
type AccountingReceiver interface {
	TryAttemptBatch([]usage.AttemptObservation) (<-chan bool, error)
	TryRemoteAccountingHealth(string, telemetry.AccountingHealth) (<-chan bool, error)
	MarkAccountingIncomplete(string)
}

// receiveAccounting runs in a connection-owned background goroutine. Waiting
// for persistence is confined to this RPC, never the ordinary RunTurn stream.
func receiveAccounting(ctx context.Context, client wire.WorkerClient, sink AccountingReceiver, source string, ready func()) error {
	return receiveAccountingControlled(ctx, client, sink, source, ready, nil)
}

var errAccountingDrainIncomplete = errors.New("worker accounting drain incomplete")

func receiveAccountingControlled(ctx context.Context, client wire.WorkerClient, sink AccountingReceiver, source string, ready func(), drain <-chan struct{}) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if sink == nil || source == "" || len(source) > 256 {
		return fmt.Errorf("invalid worker accounting receiver")
	}
	stream, err := client.Accounting(ctx, grpc.MaxCallRecvMsgSize(maxAccountingWireBytes), grpc.MaxCallSendMsgSize(4096))
	if err != nil {
		return err
	}
	defer stream.CloseSend()
	var sendMu sync.Mutex
	send := func(r *wire.WorkerAccountingReceipt) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(r)
	}

	if err = send(&wire.WorkerAccountingReceipt{}); err != nil {
		return err
	}
	handshake, err := stream.Recv()
	if err != nil {
		return err
	}
	if handshake.BatchId != "" || len(handshake.Observations) != 0 || handshake.Health != nil || handshake.DrainFinished || handshake.DrainError != "" {
		return fmt.Errorf("invalid worker accounting readiness message")
	}
	if ready != nil {
		ready()
	}
	if drain != nil {
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-drain:
				if err := send(&wire.WorkerAccountingReceipt{Drain: true}); err != nil {
					cancel()
				}
			}
		}()
	}
	workerID := "worker/" + source
	for {
		batch, err := stream.Recv()
		if err != nil {
			return err
		}
		if batch.DrainFinished {
			select {
			case <-drain:
			default:
				return fmt.Errorf("unsolicited worker accounting drain")
			}
			if batch.BatchId != "" || len(batch.Observations) != 0 || batch.Health != nil || len(batch.DrainError) > 1024 {
				return fmt.Errorf("invalid worker accounting drain result")
			}
			if batch.DrainError != "" {
				sink.MarkAccountingIncomplete("worker accounting drain incomplete")
				return errAccountingDrainIncomplete
			}
			return nil
		}
		if batch.DrainError != "" {
			return fmt.Errorf("invalid worker accounting drain error")
		}
		if batch.BatchId == "" || len(batch.BatchId) > 1024 {
			return fmt.Errorf("invalid worker accounting batch identity")
		}
		var receipt <-chan bool
		if batch.Health != nil {
			var id string
			var h telemetry.AccountingHealth
			id, h, err = accountingHealthFromWire(batch)
			if err == nil {
				if h.Lost > 0 || h.Uncertain > 0 || h.CoverageIncomplete {
					sink.MarkAccountingIncomplete("worker accounting reports incomplete coverage")
				}
				// Scope remote identities to their host-owned connection identity. A worker
				// cannot overwrite the host writer's status or another worker's status.
				receipt, err = sink.TryRemoteAccountingHealth(workerID+"/"+id, h)
			}
		} else {
			var observations []usage.AttemptObservation
			observations, err = accountingBatchFromWire(batch)
			if err == nil {
				for i := range observations {
					observations[i].Attribution.WorkerID = workerID
				}
				receipt, err = sink.TryAttemptBatch(observations)
			}
		}
		ack := &wire.WorkerAccountingReceipt{BatchId: batch.BatchId}
		if err != nil {
			ack.Retryable = errors.Is(err, telemetry.ErrAccountingCapacity)
			ack.Error = "accounting batch not admitted"
		} else {
			timer := time.NewTimer(2 * time.Second)
			select {
			case committed := <-receipt:
				ack.Persisted = committed
				ack.Retryable = !committed
				if !committed {
					ack.Error = "accounting persistence not acknowledged"
				}
			case <-timer.C:
				ack.Retryable = true
				ack.Error = "accounting persistence acknowledgment timed out"
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
			timer.Stop()
		}
		if err = send(ack); err != nil {
			return err
		}
	}
}
