package worker

// host_callback_join_test.go — regression coverage for the drain-loop
// callback goroutines (permission, auth, credential, open-runtime, runtime
// restart, MCP, autonomy, session model, profile).
//
// Contract under test: when the drain loop returns (TurnDone + stream close),
// RunTurn must FIRST cancel the local turn context so ctx-aware callbacks and
// blocked stream.Send calls unwind, THEN join every callback goroutine, and
// only THEN release the worker (cleanupFn / pool release). Returning from
// RunTurn means every callback has actually ended.
//
// Reproduction: a fake worker (bufconn, no real credentials involved) sends
// one MCP callback request and then immediately sends TurnDone and closes the
// stream while the host's MCP callback is still blocked. Against the baseline
// (fire-and-forget goroutines + caller ctx), RunTurn returned early with the
// callback still running; this test fails on that behavior and passes once the
// join + turn-context fix is in place.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/hostsvc/permissions"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// mcpThenDoneWorker is a fake worker: it receives StartTurn, asks the host to
// run one proxied MCP tool, and then ends the turn (TurnDone + stream close)
// before the host-side callback completes.
type mcpThenDoneWorker struct {
	proto.UnimplementedWorkerServer
}

func (w *mcpThenDoneWorker) RunTurn(stream proto.Worker_RunTurnServer) error {
	if _, err := stream.Recv(); err != nil { // StartTurn
		return err
	}
	if err := stream.Send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_McpRequest{
		McpRequest: &proto.McpCallRequest{Id: "1", Name: "fixture__blocked", ArgsJson: []byte(`{}`)},
	}}); err != nil {
		return err
	}
	// End the turn while the host's MCP callback is still executing.
	return stream.Send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_Done{Done: &proto.TurnDone{FinalText: "done"}}})
}

// TestRunTurnWaitsForCallbackGoroutinesBeforeReturn drives the MCP callback
// (chosen over credential/auth paths so no real credentials are touched):
//
//   - the host's MCP bridge blocks on the callback context (only the turn
//     context cancellation can release it);
//   - the worker ends the turn anyway;
//   - RunTurn must return, and by the time it returns the callback must have
//     been released and observed context.Canceled on the turn context.
func TestRunTurnWaitsForCallbackGoroutinesBeforeReturn(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	proto.RegisterWorkerServer(server, &mcpThenDoneWorker{})
	go server.Serve(listener)
	defer server.Stop()

	dial := func(ctx context.Context) (*grpc.ClientConn, error) {
		return grpc.NewClient("passthrough:///bufconn",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return listener.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	host := newWorkerRunnerWithDial(nil, cfgsvc.New("", config.Defaults(), secrets.NewMemory()), permissions.New(nil, nil, nil), dial)

	callbackStarted := make(chan struct{})
	callbackReleased := make(chan struct{})
	var callbackCtxErr error
	host.SetMCPBridge(
		func() []McpToolAdvert {
			return []McpToolAdvert{{Name: "fixture__blocked", Description: "blocks until turn ctx cancels"}}
		},
		func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
			close(callbackStarted)
			<-ctx.Done() // nothing but the turn-context cancellation can release this
			callbackCtxErr = ctx.Err()
			close(callbackReleased)
			return json.RawMessage(`{}`), nil
		},
	)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	type turnOutcome struct {
		res runner.Result
		err error
	}
	turnDone := make(chan turnOutcome, 1)
	go func() {
		res, err := host.RunTurn(ctx, runner.Request{
			ConversationID: "callback-join",
			WorkDir:        t.TempDir(),
			Gen:            1,
		}, nil, nil, nil)
		turnDone <- turnOutcome{res, err}
	}()

	// 1. The worker's McpRequest actually reached the host's MCP bridge.
	select {
	case <-callbackStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("host MCP callback never started — McpRequest never reached the bridge")
	}

	// 2. RunTurn returns promptly after TurnDone + stream close (no hang from
	// the join — the turn-context cancellation must unblock the callback).
	select {
	case out := <-turnDone:
		if out.err != nil {
			t.Fatalf("RunTurn: %v", out.err)
		}
		// 3. But it must NOT have returned while the callback was still
		// running: baseline behavior (fire-and-forget goroutine on the caller's
		// ctx) fails here.
		select {
		case <-callbackReleased:
		default:
			t.Fatal("RunTurn returned while the MCP callback goroutine was still running")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunTurn did not return after TurnDone — the callback join deadlocked (turn-context cancellation must release it)")
	}

	// 4. The cancellation is observable: the callback unblocked via
	// ctx.Done() and saw exactly context.Canceled on the turn context.
	if !errors.Is(callbackCtxErr, context.Canceled) {
		t.Errorf("callback ctx error = %v, want context.Canceled (turn-context cancellation must be observable to callbacks)", callbackCtxErr)
	}
}
