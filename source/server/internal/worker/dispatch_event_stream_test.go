package worker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	pkgcfg "cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// Drive the real host receive switch over protobuf/gRPC, including child
// creation before the acknowledged event. No model or external service needed.
type dispatchEvidenceStream struct {
	proto.UnimplementedWorkerServer
}

func (*dispatchEvidenceStream) RunTurn(stream proto.Worker_RunTurnServer) error {
	start, err := stream.Recv()
	if err != nil {
		return err
	}
	parent := start.GetStart().GetConversationId()
	sendEvent := func(id string, seq int64, wantError bool) error {
		if err := stream.Send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_DispatchEvent{DispatchEvent: &proto.DispatchEventRequest{Id: uint64(seq), ConversationId: id, Seq: seq, Kind: "model_request", Iteration: int32(seq), PayloadJson: fmt.Sprintf(`{"iteration":%d}`, seq)}}}); err != nil {
			return err
		}
		for {
			ack, err := stream.Recv()
			if err != nil {
				return err
			}
			if response := ack.GetDispatchEventResponse(); response != nil {
				if (response.GetError() != "") != wantError {
					return fmt.Errorf("unexpected acknowledgement for %s: %s", id, response.GetError())
				}
				return nil
			}
		}
	}
	if err := sendEvent(parent, 1, true); err != nil {
		return err
	}
	if err := sendEvent("foreign", 1, true); err != nil {
		return err
	}
	for _, child := range []string{"child-a", "child-b"} {
		if err := stream.Send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_EnsureSubagent{EnsureSubagent: &proto.EnsureSubagentConversation{Id: child, ParentId: parent}}}); err != nil {
			return err
		}
		for seq := int64(1); seq <= 2; seq++ {
			if err := sendEvent(child, seq, false); err != nil {
				return err
			}
		}
	}
	return stream.Send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_Done{Done: &proto.TurnDone{}}})
}

func TestDispatchEventRealHostStream(t *testing.T) {
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []string{"parent", "foreign"} {
		if err := store.EnsureConversation(t.Context(), id, "", "model"); err != nil {
			t.Fatal(err)
		}
	}
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	proto.RegisterWorkerServer(srv, &dispatchEvidenceStream{})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()
	dial := func(ctx context.Context) (*grpc.ClientConn, error) {
		return grpc.NewClient("passthrough:///dispatch-test", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	cfg := cfgsvc.New("", pkgcfg.Config{LocusMode: "open_only"}, secrets.NewMemory())
	r := NewWorkerRunnerWithDial(nil, cfg, nil, dial).(*workerRunner)
	r.ensureSubagent = store.EnsureSubagentConversation
	events := store.(conversation.DispatchEventStore)
	r.SetDispatchEventSink(HostDispatchEventSink(events))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := r.RunTurn(ctx, runner.Request{ConversationID: "parent", Input: "test"}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"child-a", "child-b", "parent", "foreign"} {
		rows, err := events.ListDispatchEvents(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if id == "child-a" || id == "child-b" {
			want = 2
		}
		if len(rows) != want {
			t.Fatalf("%s stored %d events, want %d", id, len(rows), want)
		}
	}
}

func TestDispatchChildAuthorization(t *testing.T) {
	scope := map[string]bool{}
	calls := 0
	r := &workerRunner{ensureSubagent: func(context.Context, string, string, string, string, []string) error { calls++; return nil }}
	for _, e := range []*proto.EnsureSubagentConversation{nil, {Id: "parent", ParentId: "parent"}, {Id: "child", ParentId: "foreign"}, {Id: "same", ParentId: "same"}} {
		if err := r.ensureDispatchChild(t.Context(), e, "parent", scope); err == nil {
			t.Fatal("invalid child accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid creation reached store")
	}
	if err := r.ensureDispatchChild(t.Context(), &proto.EnsureSubagentConversation{Id: "child", ParentId: "parent"}, "parent", scope); err != nil {
		t.Fatal(err)
	}
	if err := r.ensureDispatchChild(t.Context(), &proto.EnsureSubagentConversation{Id: "nested", ParentId: "child"}, "parent", scope); err != nil {
		t.Fatal(err)
	}
	r.ensureSubagent = func(context.Context, string, string, string, string, []string) error {
		return errors.New("disk failed")
	}
	if err := r.ensureDispatchChild(t.Context(), &proto.EnsureSubagentConversation{Id: "failed", ParentId: "parent"}, "parent", scope); err == nil || scope["failed"] {
		t.Fatal("failed creation authorized")
	}
	if scope["parent"] || !scope["child"] || !scope["nested"] {
		t.Fatal(scope)
	}
}
