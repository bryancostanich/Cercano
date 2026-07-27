package server

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/proto"
)

// drainTestAgent is a minimal AgentServer whose StreamProcessRequest sends one
// token, waits on release, then sends a final token — letting tests hold an
// RPC in flight across a shutdown.
type drainTestAgent struct {
	proto.UnimplementedAgentServer
	release chan struct{} // closed by the test to let the handler finish
}

func (a *drainTestAgent) StreamProcessRequest(_ *proto.ProcessRequestRequest, stream proto.Agent_StreamProcessRequestServer) error {
	if err := stream.Send(tokenDelta("started")); err != nil {
		return err
	}
	<-a.release
	return stream.Send(tokenDelta("finished"))
}

func tokenDelta(s string) *proto.StreamProcessResponse {
	return &proto.StreamProcessResponse{Payload: &proto.StreamProcessResponse_TokenDelta{
		TokenDelta: &proto.TokenDelta{Content: s},
	}}
}

// startDrainTestServer spins up a bufconn gRPC server with the given agent and
// returns the grpc.Server plus a connected client.
func startDrainTestServer(t *testing.T, agent proto.AgentServer) (*grpc.Server, proto.AgentClient) {
	t.Helper()
	l := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	proto.RegisterAgentServer(gs, agent)
	go gs.Serve(l)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return l.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufnet: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return gs, proto.NewAgentClient(conn)
}

// An in-flight streaming turn must complete during the drain window: the
// client receives every message and a clean EOF, never an Unavailable error.
// This is the regression test for the launcher-kill EOF bug: SIGTERM now
// drains instead of severing streams.
func TestDrainThenStop_InFlightStreamCompletes(t *testing.T) {
	agent := &drainTestAgent{release: make(chan struct{})}
	gs, client := startDrainTestServer(t, agent)

	stream, err := client.StreamProcessRequest(context.Background(), &proto.ProcessRequestRequest{Input: "hi"})
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if msg, err := stream.Recv(); err != nil || msg.GetTokenDelta().GetContent() != "started" {
		t.Fatalf("first recv = %q, %v; want started", msg.GetTokenDelta().GetContent(), err)
	}

	drained := make(chan bool, 1)
	go func() { drained <- DrainThenStop(gs, 10*time.Second) }()

	// Let the handler finish while the drain is waiting on it.
	time.Sleep(50 * time.Millisecond)
	close(agent.release)

	if msg, err := stream.Recv(); err != nil || msg.GetTokenDelta().GetContent() != "finished" {
		t.Fatalf("second recv = %q, %v; want finished", msg.GetTokenDelta().GetContent(), err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("final recv err = %v; want io.EOF", err)
	}

	select {
	case ok := <-drained:
		if !ok {
			t.Fatal("DrainThenStop = false; want true (graceful drain completed)")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DrainThenStop did not return after stream completed")
	}
}

// A handler that never finishes must not wedge shutdown forever: after the
// grace period DrainThenStop hard-stops and reports false.
func TestDrainThenStop_HardStopsAfterGrace(t *testing.T) {
	agent := &drainTestAgent{release: make(chan struct{})} // wedged until cleanup
	t.Cleanup(func() { close(agent.release) })             // let leaked goroutines finish
	gs, client := startDrainTestServer(t, agent)

	stream, err := client.StreamProcessRequest(context.Background(), &proto.ProcessRequestRequest{Input: "hi"})
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("first recv: %v", err)
	}

	start := time.Now()
	if ok := DrainThenStop(gs, 100*time.Millisecond); ok {
		t.Fatal("DrainThenStop = true; want false (grace expired)")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("hard stop took %v; want ~grace period", elapsed)
	}

	if _, err := stream.Recv(); status.Code(err) != codes.Unavailable {
		t.Fatalf("recv after hard stop = %v; want Unavailable", err)
	}
}

// closeAll ends every subscriber channel and empties the hub; previously
// returned unsubscribe funcs stay safe to call (no double-close panic).
func TestEventHub_CloseAll(t *testing.T) {
	h := newEventHub()
	ch1, unsub1 := h.subscribe()
	ch2, _ := h.subscribe()

	h.closeAll()

	for i, ch := range []<-chan *proto.ClientEvent{ch1, ch2} {
		if _, ok := <-ch; ok {
			t.Errorf("subscriber %d channel still open after closeAll", i)
		}
	}
	if got := h.subscriberCount(); got != 0 {
		t.Errorf("subscriberCount = %d after closeAll, want 0", got)
	}
	unsub1() // must not panic on already-closed subscriber
}

// fakeSubscribeStream satisfies proto.Agent_SubscribeEventsServer just enough
// for the handler loop: a live context and a Send that always succeeds.
type fakeSubscribeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f *fakeSubscribeStream) Context() context.Context        { return f.ctx }
func (f *fakeSubscribeStream) Send(_ *proto.ClientEvent) error { return nil }
func (f *fakeSubscribeStream) SendMsg(_ any) error             { return nil }
func (f *fakeSubscribeStream) RecvMsg(_ any) error             { return nil }

// BeginShutdown must end a standing SubscribeEvents handler with a nil error —
// otherwise GracefulStop blocks forever on attached clients.
func TestServer_BeginShutdown_EndsSubscribeEvents(t *testing.T) {
	s := &Server{events: newEventHub()}
	done := make(chan error, 1)
	go func() {
		done <- s.SubscribeEvents(&proto.SubscribeEventsRequest{}, &fakeSubscribeStream{ctx: context.Background()})
	}()

	// Wait until the handler has actually subscribed.
	deadline := time.Now().Add(2 * time.Second)
	for s.events.subscriberCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("handler never subscribed")
		}
		time.Sleep(5 * time.Millisecond)
	}

	s.BeginShutdown()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SubscribeEvents returned %v after BeginShutdown; want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SubscribeEvents still blocked after BeginShutdown")
	}
}

// BeginShutdown on a server with no event hub must not panic.
func TestServer_BeginShutdown_NilHub(t *testing.T) {
	(&Server{}).BeginShutdown()
}

func TestServer_EnableIdleShutdownRunsAfterLastSubscriberDisconnects(t *testing.T) {
	s := &Server{events: newEventHub()}
	done := make(chan struct{}, 1)
	s.EnableIdleShutdown(0, func() { done <- struct{}{} })

	_, unsub1 := s.events.subscribe()
	_, unsub2 := s.events.subscribe()
	unsub1()
	select {
	case <-done:
		t.Fatal("idle shutdown fired before the last subscriber disconnected")
	case <-time.After(20 * time.Millisecond):
	}

	unsub2()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idle shutdown did not fire after last subscriber disconnected")
	}
}

func TestServer_EnableIdleShutdownRechecksAfterGrace(t *testing.T) {
	s := &Server{events: newEventHub()}
	done := make(chan struct{}, 1)
	s.EnableIdleShutdown(30*time.Millisecond, func() { done <- struct{}{} })

	_, unsub1 := s.events.subscribe()
	unsub1()
	_, unsub2 := s.events.subscribe()
	defer unsub2()

	select {
	case <-done:
		t.Fatal("idle shutdown fired despite a subscriber reconnecting during grace")
	case <-time.After(80 * time.Millisecond):
	}
}

type shutdownRuntimeProvider struct {
	stopped []string
}

func (p *shutdownRuntimeProvider) Name() string { return "mistralrs" }
func (p *shutdownRuntimeProvider) Capabilities() localruntime.RuntimeCapabilities {
	return localruntime.RuntimeCapabilities{ManagedProcesses: true, CanStop: true}
}
func (p *shutdownRuntimeProvider) Discover(context.Context) ([]localruntime.ModelRecord, error) {
	return nil, nil
}
func (p *shutdownRuntimeProvider) Start(context.Context, localruntime.StartRequest, localruntime.LogSink) (*localruntime.InstanceRecord, error) {
	return nil, nil
}
func (p *shutdownRuntimeProvider) Stop(_ context.Context, id string) error {
	p.stopped = append(p.stopped, id)
	return nil
}
func (p *shutdownRuntimeProvider) Probe(context.Context, string) (*localruntime.InstanceHealth, error) {
	return nil, nil
}

func TestServerShutdownStopsRuntimeInstances(t *testing.T) {
	provider := &shutdownRuntimeProvider{}
	mgr := localruntime.NewManager()
	mgr.RegisterProvider(provider)
	mgr.UpdateInstance(localruntime.InstanceRecord{
		ID:      "mistralrs:abc:12345",
		Runtime: "mistralrs",
		ModelID: "mistralrs:catalog:qwen3-30b-a3b-instruct-2507",
		State:   localruntime.InstanceRunning,
	})
	mgr.UpdateInstance(localruntime.InstanceRecord{
		ID:      "mistralrs:stopped:12346",
		Runtime: "mistralrs",
		ModelID: "mistralrs:catalog:qwen3-30b-a3b-instruct-2507",
		State:   localruntime.InstanceStopped,
	})

	srv := &Server{}
	srv.SetRuntimeManager(mgr)
	srv.Shutdown()

	if len(provider.stopped) != 1 || provider.stopped[0] != "mistralrs:abc:12345" {
		t.Fatalf("stopped instances = %#v, want only running instance", provider.stopped)
	}
	instances, err := mgr.Instances(context.Background())
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	for _, inst := range instances {
		if inst.ID == "mistralrs:abc:12345" && inst.State != localruntime.InstanceStopped {
			t.Fatalf("shutdown did not mark instance stopped: %#v", inst)
		}
	}
}
