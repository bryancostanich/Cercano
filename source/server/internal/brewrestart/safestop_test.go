package brewrestart

import (
	"context"
	"net"
	"os"
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
// Client side of the update-related stop: the coordinator must issue the
// bounded ShutdownAgentWhenIdle safe-stop RPC (expected PID from the
// kernel-verified ownership inspection) and NEVER fall back to the legacy
// fire-and-forget ShutdownAgent bounce. These tests use bufconn fakes of the
// agent so nothing here starts, stops or signals a real process.
// ---------------------------------------------------------------------------

// safeStopFake is a bufconn agent. It enforces the same identity guard as the
// real server (expected_pid must equal agentPID), records every RPC, and can
// be configured as busy (blocks until the request deadline), old (only the
// legacy RPC is implemented), or idle (commits immediately).
type safeStopFake struct {
	proto.UnimplementedAgentServer
	mu       sync.Mutex
	agentPID int64
	busy     bool
	legacy   int
	stopPID  int64
	stopped  int
}

func (f *safeStopFake) ShutdownAgent(context.Context, *proto.ShutdownAgentRequest) (*proto.ShutdownAgentResponse, error) {
	f.mu.Lock()
	f.legacy++
	f.mu.Unlock()
	return &proto.ShutdownAgentResponse{Accepted: true}, nil
}

func (f *safeStopFake) ShutdownAgentWhenIdle(ctx context.Context, req *proto.ShutdownAgentWhenIdleRequest) (*proto.ShutdownAgentWhenIdleResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.GetExpectedPid() != f.agentPID {
		return nil, status.Errorf(codes.FailedPrecondition,
			"expected_pid %d does not match this agent process (pid %d)", req.GetExpectedPid(), f.agentPID)
	}
	if f.busy {
		<-ctx.Done()
		return nil, status.Error(codes.DeadlineExceeded, "safe stop wait timed out")
	}
	f.stopPID = req.GetExpectedPid()
	f.stopped++
	return &proto.ShutdownAgentWhenIdleResponse{Accepted: true, Message: "safe stop committed"}, nil
}

func (f *safeStopFake) calls() (legacy, stopped int, stopPID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.legacy, f.stopped, f.stopPID
}

func startSafeStopFake(t *testing.T, fake proto.AgentServer) proto.AgentClient {
	t.Helper()
	l := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	proto.RegisterAgentServer(gs, fake)
	go func() { _ = gs.Serve(l) }()
	t.Cleanup(gs.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return l.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufnet: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return proto.NewAgentClient(conn)
}

// Active work: the safe-stop wait times out at the bounded deadline. The work
// is left running (no stop commit), the legacy bounce is never used, and the
// caller gets the typed busy diagnostic.
func TestSafeStopRequestBusyDeadlineLeavesAgentRunningWithoutLegacyCalls(t *testing.T) {
	fake := &safeStopFake{agentPID: 1234, busy: true}
	client := startSafeStopFake(t, fake)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := safeStopRequest(ctx, client, 1234, "Homebrew installation updated")
	if !isSafeStopBusy(err) {
		t.Fatalf("busy agent: err=%v, want typed busy diagnostic", err)
	}
	legacy, stopped, _ := fake.calls()
	if legacy != 0 || stopped != 0 {
		t.Fatalf("legacy=%d stopped=%d, want no stop of any kind", legacy, stopped)
	}
	// The fake is still serving: the agent was left alive and untouched.
	if _, err := client.GetConfig(context.Background(), &proto.GetConfigRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("unrelated probe after busy wait: %v", err)
	}
}

// oldAgentFake models a released agent binary that predates the safe-stop
// method: only the legacy ShutdownAgent RPC is implemented; everything else,
// including ShutdownAgentWhenIdle, answers codes.Unimplemented.
type oldAgentFake struct {
	proto.UnimplementedAgentServer
	mu     sync.Mutex
	legacy int
}

func (f *oldAgentFake) ShutdownAgent(context.Context, *proto.ShutdownAgentRequest) (*proto.ShutdownAgentResponse, error) {
	f.mu.Lock()
	f.legacy++
	f.mu.Unlock()
	return &proto.ShutdownAgentResponse{Accepted: true}, nil
}

// An agent binary predating the safe-stop method answers Unimplemented. The
// coordinator must surface the typed unsupported diagnostic with actionable
// guidance and must NEVER fall back to the legacy ShutdownAgent bounce.
func TestSafeStopRequestUnimplementedNeverFallsBackToLegacy(t *testing.T) {
	fake := &oldAgentFake{}
	client := startSafeStopFake(t, fake)

	err := safeStopRequest(context.Background(), client, os.Getpid(), "Homebrew installation updated")
	if !isSafeStopUnsupported(err) {
		t.Fatalf("old agent: err=%v, want typed unsupported diagnostic", err)
	}
	if fake.legacy != 0 {
		t.Fatalf("legacy=%d, want zero legacy calls after Unimplemented", fake.legacy)
	}
}

// The happy path: an idle agent verifies the expected PID from the ownership
// inspection and commits the safe stop exactly once.
func TestSafeStopRequestIdleCommitsOnceWithVerifiedPID(t *testing.T) {
	fake := &safeStopFake{agentPID: 4321}
	client := startSafeStopFake(t, fake)

	if err := safeStopRequest(context.Background(), client, 4321, "Homebrew installation updated"); err != nil {
		t.Fatalf("idle commit: %v", err)
	}
	if err := safeStopRequest(context.Background(), client, 4321, "Homebrew installation updated"); err != nil {
		t.Fatalf("repeat commit: %v", err)
	}
	legacy, stopped, stopPID := fake.calls()
	if legacy != 0 || stopped != 2 || stopPID != 4321 {
		t.Fatalf("legacy=%d stopped=%d stopPID=%d", legacy, stopped, stopPID)
	}
}

// A wrong expected PID is a refusal (FailedPrecondition): no stop of any
// kind, no busy/unsupported misclassification, no legacy fallback.
func TestSafeStopRequestWrongPIDIsRefusedWithoutAnyStop(t *testing.T) {
	fake := &safeStopFake{agentPID: 100}
	client := startSafeStopFake(t, fake)

	err := safeStopRequest(context.Background(), client, 999, "Homebrew installation updated")
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("wrong pid: err=%v, want FailedPrecondition", err)
	}
	if isSafeStopBusy(err) || isSafeStopUnsupported(err) {
		t.Fatalf("wrong pid misclassified as busy/unsupported: %v", err)
	}
	legacy, stopped, _ := fake.calls()
	if legacy != 0 || stopped != 0 {
		t.Fatalf("legacy=%d stopped=%d, want no stop after PID refusal", legacy, stopped)
	}
}

// TestSafeStopRequestNoAgent verifies the RPC layer's contribution to the
// no-agent contract at the coordinator seam: with no server listening the
// request cannot succeed, and the typed skip diagnostics never fire on a
// plain dial failure.
func TestSafeStopRequestNoAgentIsNotASkipDiagnostic(t *testing.T) {
	l := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	go func() { _ = gs.Serve(l) }()
	gs.Stop() // nothing is listening on the buffer anymore
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return l.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client := proto.NewAgentClient(conn)

	err = safeStopRequest(context.Background(), client, 1, "Homebrew installation updated")
	if err == nil || isSafeStopBusy(err) || isSafeStopUnsupported(err) {
		t.Fatalf("no agent: err=%v, want a plain failure not a skip diagnostic", err)
	}
}
