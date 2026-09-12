package worker

import (
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/hostsvc/permissions"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	"context"
	"encoding/json"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"net"
	"testing"
	"time"
)

type runtimeToolWorker struct {
	proto.UnimplementedWorkerServer
	debug bool
}

func (w *runtimeToolWorker) RunTurn(stream proto.Worker_RunTurnServer) error {
	start, err := stream.Recv()
	if err != nil {
		return err
	}
	if start.GetStart().GetDebugMode() != w.debug {
		return fmt.Errorf("debug advertisement flag lost")
	}
	if err := stream.Send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_PermRequest{PermRequest: &proto.PermissionRequest{Id: 1, ToolUseId: "restart", Name: "restart_runtime", Tier: "X", ArgsJson: `{"instance_id":"exact-instance"}`}}}); err != nil {
		return err
	}
	permission, err := stream.Recv()
	if err != nil {
		return err
	}
	if !permission.GetPermResponse().GetAllow() {
		return stream.Send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_Done{Done: &proto.TurnDone{FinalText: "denied"}}})
	}
	sender := newSender(stream)
	defer sender.close()
	control := newStreamRuntimeControl(sender)
	result := make(chan string, 1)
	go func() {
		raw, err := control.Restart(stream.Context(), "exact-instance")
		if err != nil {
			result <- err.Error()
		} else {
			result <- string(raw)
		}
	}()
	reply, err := stream.Recv()
	if err != nil {
		return err
	}
	control.deliver(reply.GetRuntimeResponse())
	sender.send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_Done{Done: &proto.TurnDone{FinalText: <-result}}})
	return nil
}
func TestRuntimeControlRoundTripsWithoutDependingOnDebugAdvertisement(t *testing.T) {
	for _, debug := range []bool{false, true} {
		for _, allow := range []bool{false, true} {
			t.Run(fmt.Sprintf("debug=%v/allow=%v", debug, allow), func(t *testing.T) {
				listener := bufconn.Listen(1 << 20)
				server := grpc.NewServer()
				proto.RegisterWorkerServer(server, &runtimeToolWorker{debug: debug})
				go server.Serve(listener)
				defer server.Stop()
				dial := func(ctx context.Context) (*grpc.ClientConn, error) {
					return grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
				}
				host := newWorkerRunnerWithDial(nil, cfgsvc.New("", config.Defaults(), secrets.NewMemory()), permissions.New(nil, nil, nil), dial)
				calls, confirmations := 0, 0
				host.restartRuntime = func(_ context.Context, id string) (json.RawMessage, error) {
					calls++
					if id != "exact-instance" {
						t.Errorf("wrong instance %q", id)
					}
					return json.RawMessage(`{"ok":true,"state":"running"}`), nil
				}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				result, err := host.RunTurn(ctx, runner.Request{ConversationID: "runtime-test", DebugMode: debug}, nil, func(_ context.Context, _ string, name string, _ json.RawMessage, tier llm.Permission, _ bool) (bool, error) {
					confirmations++
					if name != "restart_runtime" || tier != llm.PermX {
						t.Error("confirmation lost")
					}
					return allow, nil
				}, nil)
				if err != nil {
					t.Fatal(err)
				}
				want := 0
				if allow {
					want = 1
				}
				if calls != want || confirmations != 1 {
					t.Fatalf("calls=%d confirmations=%d", calls, confirmations)
				}
				if allow && result.FinalText != `{"ok":true,"state":"running"}` {
					t.Fatalf("result lost: %s", result.FinalText)
				}
			})
		}
	}
}
func TestRuntimeControlCancellationBeforeSendDoesNotRestart(t *testing.T) {
	sender := &sender{ch: make(chan *proto.WorkerToHost, 1)}
	control := newStreamRuntimeControl(sender)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := control.Restart(ctx, "one"); err == nil {
		t.Fatal("cancellation lost")
	}
	if len(sender.ch) != 0 {
		t.Fatal("already-cancelled tool issued a restart")
	}
}
func TestRuntimeControlCancellationCleansWaiter(t *testing.T) {
	sender := &sender{ch: make(chan *proto.WorkerToHost, 1)}
	control := newStreamRuntimeControl(sender)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := control.Restart(ctx, "one"); done <- err }()
	request := <-sender.ch
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancellation lost")
	}
	control.deliver(&proto.RuntimeRestartToolResponse{Id: request.GetRuntimeRequest().GetId()})
	control.mu.Lock()
	defer control.mu.Unlock()
	if len(control.pending) != 0 {
		t.Fatal("pending waiter leaked")
	}
}
