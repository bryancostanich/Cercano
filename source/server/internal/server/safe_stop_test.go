package server

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"cercano/source/server/pkg/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// ---------------------------------------------------------------------------
// Safe stop (ShutdownAgentWhenIdle): bounded one-shot stop with observed idle
// tracking, a positive-PID identity guard, a REQUIRED configured stop path,
// and no cancellation of active work. All tests run on real bufconn gRPC
// servers with the real admission interceptors wired exactly like main; the
// stop path is always an injected FAKE so no test ever signals or kills its
// own host process.
// ---------------------------------------------------------------------------

// fakeStopRecorder is the injectable process-stop callback: it records
// invocations (with reasons) without any process-side effect.
type fakeStopRecorder struct {
	mu      sync.Mutex
	count   int
	reasons []string
}

func (f *fakeStopRecorder) stop(reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count++
	f.reasons = append(f.reasons, reason)
}

func (f *fakeStopRecorder) invocations() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}

func (f *fakeStopRecorder) lastReason() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reasons) == 0 {
		return ""
	}
	return f.reasons[len(f.reasons)-1]
}

// safeStopFixture is a real Server on a bufconn gRPC server with the same
// interceptor chain main wires (recovery outermost, admission inner), plus a
// fake stop callback installed via the production seam.
type safeStopFixture struct {
	srv    *Server
	stop   *fakeStopRecorder
	client proto.AgentClient
	gs     *grpc.Server
}

func startSafeStopServer(t *testing.T, configure func(s *Server, stop *fakeStopRecorder)) *safeStopFixture {
	t.Helper()
	s := NewServer(nil, nil, nil, nil, nil)
	stop := &fakeStopRecorder{}
	if configure != nil {
		configure(s, stop)
	} else {
		s.SetProcessStopRequester(stop.stop)
	}
	l := bufconn.Listen(1 << 20)
	gs := grpc.NewServer(
		grpc.ChainUnaryInterceptor(RecoveryUnaryInterceptor(), s.UpdateAdmissionUnaryInterceptor()),
		grpc.ChainStreamInterceptor(RecoveryStreamInterceptor(), s.UpdateAdmissionStreamInterceptor()),
	)
	proto.RegisterAgentServer(gs, s)
	go func() { _ = gs.Serve(l) }()
	t.Cleanup(gs.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return l.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufnet: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &safeStopFixture{srv: s, stop: stop, client: proto.NewAgentClient(conn), gs: gs}
}

func safeStopReq(pid int64) *proto.ShutdownAgentWhenIdleRequest {
	return &proto.ShutdownAgentWhenIdleRequest{ExpectedPid: pid, Reason: "test"}
}

// admissionOpen asserts new update-relevant work is still admitted (the gate
// was left untouched by a refused request).
func admissionOpen(t *testing.T, s *Server) {
	t.Helper()
	work, err := s.updateWork.enter()
	if err != nil {
		t.Fatalf("admission not open: %v", err)
	}
	work()
}

func TestSafeStopRejectsMalformedRequestBeforeAnyEffect(t *testing.T) {
	f := startSafeStopServer(t, nil)
	ctx := context.Background()

	// Nil request — rejected before anything happens.
	if _, err := f.srv.ShutdownAgentWhenIdle(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("nil request: %v", err)
	}
	// Non-positive PIDs are invalid: the identity guard is meaningless without
	// a real process identity.
	for _, pid := range []int64{0, -1} {
		if _, err := f.srv.ShutdownAgentWhenIdle(ctx, safeStopReq(pid)); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("pid %d: %v", pid, err)
		}
	}
	if got := f.stop.invocations(); got != 0 {
		t.Fatalf("malformed requests invoked the stop path %d times", got)
	}
	admissionOpen(t, f.srv)
}

func TestSafeStopRejectsWrongPIDWithoutEffect(t *testing.T) {
	f := startSafeStopServer(t, nil)

	_, err := f.client.ShutdownAgentWhenIdle(context.Background(), safeStopReq(int64(os.Getpid())+1))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("wrong pid: %v", err)
	}
	// Rejection happened before any effect: no callback, no seal, and the
	// (configured) stop path was never asked to run.
	if got := f.stop.invocations(); got != 0 {
		t.Fatalf("wrong pid invoked the stop path %d times", got)
	}
	admissionOpen(t, f.srv)

	// The identity guard holds even after a commit exists: a mismatched PID is
	// never rewarded with the idempotent "already committed" response.
	if _, err := f.client.ShutdownAgentWhenIdle(context.Background(), safeStopReq(int64(os.Getpid()))); err != nil {
		t.Fatalf("committing request: %v", err)
	}
	_, err = f.client.ShutdownAgentWhenIdle(context.Background(), safeStopReq(int64(os.Getpid())+1))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("wrong pid after commit: %v", err)
	}
}

func TestSafeStopRefusesWithoutConfiguredStopPath(t *testing.T) {
	// No SetProcessStopRequester: the handler must refuse rather than guess,
	// and must never fall back to self-signaling the process.
	f := startSafeStopServer(t, func(s *Server, _ *fakeStopRecorder) {})

	_, err := f.client.ShutdownAgentWhenIdle(context.Background(), safeStopReq(int64(os.Getpid())))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("unconfigured stop path: %v", err)
	}
	if got := f.stop.invocations(); got != 0 {
		t.Fatalf("unconfigured stop path invoked the callback %d times", got)
	}
	admissionOpen(t, f.srv)
}

func TestSafeStopRefusesIncompleteCoverageWithoutCallback(t *testing.T) {
	f := startSafeStopServer(t, func(s *Server, stop *fakeStopRecorder) {
		// Configured stop path, but a simulated gap in observed coverage
		// (e.g., a work source that never installed its tracking helper):
		// refuse rather than claim safety on a guess.
		s.SetProcessStopRequester(stop.stop)
		s.setInjectedUpdateTrackingErr("runtime", errors.New("downloads unbound"))
	})

	_, err := f.client.ShutdownAgentWhenIdle(context.Background(), safeStopReq(int64(os.Getpid())))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("incomplete coverage: %v", err)
	}
	if !strings.Contains(status.Convert(err).Message(), "safe stop refused") {
		t.Fatalf("refusal message = %q, want the safe-stop refusal wrapper", status.Convert(err).Message())
	}
	if got := f.stop.invocations(); got != 0 {
		t.Fatalf("refused request invoked the stop path %d times", got)
	}
	admissionOpen(t, f.srv)
}

func TestSafeStopBusyWorkIsWaitedOnNeverCancelled(t *testing.T) {
	f := startSafeStopServer(t, nil)
	// Active update-relevant work: the safe stop must wait for it, never cancel.
	work, err := f.srv.updateWork.enter()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	resp, err := f.client.ShutdownAgentWhenIdle(ctx, safeStopReq(int64(os.Getpid())))
	if resp != nil || status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("busy wait did not time out: resp=%v err=%v", resp != nil, err)
	}
	if got := f.stop.invocations(); got != 0 {
		t.Fatalf("timed-out wait invoked the stop path %d times", got)
	}

	// The work was not cancelled and still owns its own lifetime: nested
	// admission still works and the held lifetime can retire on its own terms.
	nested, err := f.srv.updateWork.enter()
	if err != nil {
		t.Fatalf("busy work was disturbed by the failed wait: %v", err)
	}
	nested()
	work()

	resp, err = f.client.ShutdownAgentWhenIdle(context.Background(), safeStopReq(int64(os.Getpid())))
	if err != nil {
		t.Fatalf("commit after drain: %v", err)
	}
	if !resp.GetAccepted() {
		t.Fatalf("commit response not accepted: %+v", resp)
	}
	if got := f.stop.invocations(); got != 1 {
		t.Fatalf("stop path invocations = %d, want 1", got)
	}
}

func TestSafeStopCancelledWaitReleasesAdmission(t *testing.T) {
	f := startSafeStopServer(t, nil)
	work, err := f.srv.updateWork.enter()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := f.client.ShutdownAgentWhenIdle(ctx, safeStopReq(int64(os.Getpid())))
		done <- err
	}()
	// Give the request time to block inside the wait, then cancel it.
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; status.Code(err) != codes.Canceled {
		t.Fatalf("canceled wait: %v", err)
	}
	if got := f.stop.invocations(); got != 0 {
		t.Fatalf("canceled wait invoked the stop path %d times", got)
	}

	// A canceled pre-seal wait freed admission: new work is admitted.
	admissionOpen(t, f.srv)
	work()
}

func TestSafeStopCommitSealsAdmissionUntilProcessExit(t *testing.T) {
	f := startSafeStopServer(t, nil)
	pid := int64(os.Getpid())

	resp, err := f.client.ShutdownAgentWhenIdle(context.Background(), safeStopReq(pid))
	if err != nil || !resp.GetAccepted() {
		t.Fatalf("commit: resp=%+v err=%v", resp, err)
	}
	if got := f.stop.invocations(); got != 1 {
		t.Fatalf("stop path invocations = %d, want 1", got)
	}

	// The seal is kept through the process stop: new update-relevant work is
	// refused at the interceptor (before any handler side effect).
	_, err = f.client.ListConversations(context.Background(), &proto.ListConversationsRequest{})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("new work admitted after safe-stop commit: %v", err)
	}

	// Read-only observer streams keep serving through the drain window.
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := f.client.SubscribeEvents(ctx, &proto.SubscribeEventsRequest{})
	if err != nil {
		t.Fatalf("observer stream refused after commit: %v", err)
	}
	// The stream must not have produced an error immediately; close it cleanly.
	time.Sleep(25 * time.Millisecond)
	cancel()
	_, _ = stream.Recv()

	// Repeated requests while committed are idempotent: accepted, no new
	// callback, no duplicate commit.
	resp2, err := f.client.ShutdownAgentWhenIdle(context.Background(), safeStopReq(pid))
	if err != nil || !resp2.GetAccepted() {
		t.Fatalf("idempotent repeat: resp=%+v err=%v", resp2, err)
	}
	if got := f.stop.invocations(); got != 1 {
		t.Fatalf("idempotent repeat invoked the stop path again: %d", got)
	}
}

func TestSafeStopCallbackOnceUnderConcurrentRepeats(t *testing.T) {
	f := startSafeStopServer(t, nil)
	pid := int64(os.Getpid())

	// Several racing requests plus the committed one: exactly one commit.
	const racers = 8
	results := make(chan error, racers)
	for i := 0; i < racers; i++ {
		go func() {
			_, err := f.client.ShutdownAgentWhenIdle(context.Background(), safeStopReq(pid))
			results <- err
		}()
	}
	for i := 0; i < racers; i++ {
		if err := <-results; err != nil {
			t.Errorf("racing request: %v", err)
		}
	}
	if got := f.stop.invocations(); got != 1 {
		t.Fatalf("stop path invocations = %d, want exactly 1", got)
	}
	// And the gate stays sealed afterwards.
	if _, err := f.srv.updateWork.enter(); !errors.Is(err, errUpdateAdmissionPaused) {
		t.Fatalf("admission not sealed after concurrent commits: %v", err)
	}
}

// TestSafeStopCancelRaceDocumentsCommitBoundary pins the documented commit
// boundary: cancellation BEFORE the idle seal releases everything with no
// effect (no callback, gate free); the commit is decided atomically when the
// seal is observed, and a context canceled after that point cannot un-commit
// or unseal — the RPC response may be lost, but a repeat request observes
// "committed" and returns the idempotent accepted response.
func TestSafeStopCancelRaceDocumentsCommitBoundary(t *testing.T) {
	f := startSafeStopServer(t, nil)
	pid := int64(os.Getpid())
	work, err := f.srv.updateWork.enter()
	if err != nil {
		t.Fatal(err)
	}

	// Side A: canceled before the work drains — must fully release.
	ctxA, cancelA := context.WithCancel(context.Background())
	errA := make(chan error, 1)
	go func() {
		_, err := f.client.ShutdownAgentWhenIdle(ctxA, safeStopReq(pid))
		errA <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancelA()
	if err := <-errA; status.Code(err) != codes.Canceled {
		t.Fatalf("pre-seal cancel: %v", err)
	}
	if got := f.stop.invocations(); got != 0 {
		t.Fatalf("pre-seal cancel invoked the stop path %d times", got)
	}

	// Side B: commits once the work drains on its own terms.
	work()
	respB, err := f.client.ShutdownAgentWhenIdle(context.Background(), safeStopReq(pid))
	if err != nil || !respB.GetAccepted() {
		t.Fatalf("commit: resp=%+v err=%v", respB, err)
	}
	if got := f.stop.invocations(); got != 1 {
		t.Fatalf("stop path invocations = %d, want 1", got)
	}

	// Canceling B's request context AFTER the commit changes nothing: the
	// seal survives the RPC lifetime — admission stays closed until the
	// process actually exits (the fake callback stands in for that exit).
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	resp, err := f.client.ShutdownAgentWhenIdle(ctxB, safeStopReq(pid))
	if err != nil || !resp.GetAccepted() {
		t.Fatalf("post-commit repeat: resp=%+v err=%v", resp, err)
	}
	cancelB()
	if _, err := f.srv.updateWork.enter(); !errors.Is(err, errUpdateAdmissionPaused) {
		t.Fatalf("post-commit cancel released the seal: %v", err)
	}
	if got := f.stop.invocations(); got != 1 {
		t.Fatalf("post-commit repeat invoked the stop path again: %d", got)
	}
}

// oldSafeStopStubServer models an agent binary that predates the safe-stop
// method: every unimplemented method answers codes.Unimplemented, and one
// method is overridden to prove the server stays healthy after the attempt.
type oldSafeStopStubServer struct {
	proto.UnimplementedAgentServer
}

func (o *oldSafeStopStubServer) GetConfig(context.Context, *proto.GetConfigRequest) (*proto.GetConfigResponse, error) {
	return &proto.GetConfigResponse{}, nil
}

func TestSafeStopUnimplementedOnOldServerHasNoSideEffect(t *testing.T) {
	l := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	proto.RegisterAgentServer(gs, &oldSafeStopStubServer{})
	go func() { _ = gs.Serve(l) }()
	t.Cleanup(gs.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return l.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufnet: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := proto.NewAgentClient(conn)

	// Old agents return Unimplemented for the distinct method — never a
	// partial stop and never an Unavailable-style hang.
	_, err = client.ShutdownAgentWhenIdle(context.Background(), safeStopReq(int64(os.Getpid())))
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("old server safe stop: %v", err)
	}
	// The server is untouched and keeps serving afterwards: no side effect.
	if _, err := client.GetConfig(context.Background(), &proto.GetConfigRequest{}); err != nil {
		t.Fatalf("old server degraded after safe-stop attempt: %v", err)
	}
}
