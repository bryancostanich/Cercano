package worker

import (
	"context"
	"net"
	"testing"
	"time"

	"cercano/source/server/internal/usage"
	wire "cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func accountingTestConnection(t *testing.T) (*WorkerServer, wire.WorkerClient) {
	t.Helper()
	worker := New()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	wire.RegisterWorkerServer(server, worker)
	go server.Serve(listener)
	t.Cleanup(func() { server.Stop(); listener.Close() })
	conn, err := grpc.NewClient("passthrough:///accounting-test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return worker, wire.NewWorkerClient(conn)
}
func openAccountingStream(t *testing.T, client wire.WorkerClient) (wire.Worker_AccountingClient, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	stream, err := client.Accounting(ctx, grpc.MaxCallRecvMsgSize(maxAccountingWireBytes))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err = stream.Send(&wire.WorkerAccountingReceipt{}); err != nil {
		cancel()
		t.Fatal(err)
	}
	ready, err := stream.Recv()
	if err != nil || ready.BatchId != "" || len(ready.Observations) != 0 {
		cancel()
		t.Fatalf("handshake=%+v %v", ready, err)
	}
	t.Cleanup(cancel)
	return stream, cancel
}
func TestAccountingRPCWaitsForPersistenceWithoutBlockingTurnRPC(t *testing.T) {
	worker, client := accountingTestConnection(t)
	stream, _ := openAccountingStream(t, client)
	result := make(chan error, 1)
	go func() {
		result <- worker.accountingWriter().WriteAttempts(t.Context(), []usage.AttemptObservation{wireAttemptFixture()})
	}()
	batch, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		t.Fatalf("write completed without persistence receipt: %v", err)
	default:
	}
	// A deliberately invalid turn still has to reach its own handler and return
	// its protocol error while the accounting write is waiting for its receipt.
	// This tests RPC isolation, not a successful model turn.
	turnCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	turn, err := client.RunTurn(turnCtx)
	if err != nil {
		t.Fatal(err)
	}
	if err = turn.Send(&wire.HostToWorker{}); err != nil {
		t.Fatal(err)
	}
	response, err := turn.Recv()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ordinary RPC blocked or failed unexpectedly: %+v %v", response, err)
	}
	if err = stream.Send(&wire.WorkerAccountingReceipt{BatchId: batch.BatchId, Persisted: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("write ignored receipt")
	}
}
func TestAccountingRPCRetryUsesStableBatchID(t *testing.T) {
	worker, client := accountingTestConnection(t)
	stream, _ := openAccountingStream(t, client)
	var previous string
	for attempt := 0; attempt < 2; attempt++ {
		result := make(chan error, 1)
		go func() {
			result <- worker.accountingWriter().WriteAttempts(t.Context(), []usage.AttemptObservation{wireAttemptFixture()})
		}()
		batch, err := stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if previous != "" && batch.BatchId != previous {
			t.Fatal("retry changed batch identity")
		}
		previous = batch.BatchId
		// Unknown/out-of-order receipts cannot settle the active write.
		if err = stream.Send(&wire.WorkerAccountingReceipt{BatchId: "unrelated", Persisted: true}); err != nil {
			t.Fatal(err)
		}
		if err = stream.Send(&wire.WorkerAccountingReceipt{BatchId: batch.BatchId, Persisted: attempt == 1, Retryable: attempt == 0}); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if (err == nil) != (attempt == 1) {
				t.Fatalf("attempt %d: %v", attempt, err)
			}
		case <-time.After(time.Second):
			t.Fatal("receipt not delivered")
		}
	}
}
func TestAccountingRPCDisconnectReleasesWaiter(t *testing.T) {
	worker, client := accountingTestConnection(t)
	stream, cancel := openAccountingStream(t, client)
	result := make(chan error, 1)
	go func() {
		result <- worker.accountingWriter().WriteAttempts(t.Context(), []usage.AttemptObservation{wireAttemptFixture()})
	}()
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("disconnect claimed persistence")
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect left a writer blocked")
	}
}
