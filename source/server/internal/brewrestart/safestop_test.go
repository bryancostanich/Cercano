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
//
// The client can never prove "busy" from its own deadline: the server may
// commit the stop just before the deadline and lose the confirmation in
// transit. Deadline/transport endings are therefore typed as UNCERTAIN, never
// as a claim that the agent was busy or left alive.
// ---------------------------------------------------------------------------

// safeStopFake is a bufconn agent. It enforces the same identity guard as the
// real server (expected_pid must equal agentPID), records every RPC, and can
// be configured as busy (blocks until the request deadline), commitThenHold
// (marks the stop committed and THEN waits for the request deadline, losing
// the confirmation), unavailable (transport drop), old (only the legacy RPC
// is implemented), or idle (commits and confirms immediately).
type safeStopFake struct {
	proto.UnimplementedAgentServer
	mu             sync.Mutex
	agentPID       int64
	busy           bool
	commitThenHold bool
	unavailable    bool
	legacy         int
	stopPID        int64
	stopped        int
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
	if f.commitThenHold {
		// The server commits the stop and seals admission, then the deadline
		// fires before the confirmation can be delivered: the client's
		// DeadlineExceeded cannot distinguish this from a busy agent.
		f.stopPID = req.GetExpectedPid()
		f.stopped++
		<-ctx.Done()
		return nil, status.Error(codes.DeadlineExceeded, "safe stop wait timed out")
	}
	if f.unavailable {
		return nil, status.Error(codes.Unavailable, "transport dropped before the stop could be confirmed")
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

// Active work: the safe-stop wait times out at the bounded deadline. Nothing
// was stopped by the client, the legacy bounce is never used, and the caller
// gets the typed UNCERTAIN diagnostic — the client's own deadline never
// proves the agent was busy or left alive.
func TestSafeStopRequestBusyDeadlineIsUncertainWithoutLegacyCalls(t *testing.T) {
	fake := &safeStopFake{agentPID: 1234, busy: true}
	client := startSafeStopFake(t, fake)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := safeStopRequest(ctx, client, 1234, "Homebrew installation updated")
	if !isSafeStopUncertain(err) {
		t.Fatalf("busy agent: err=%v, want typed uncertain diagnostic", err)
	}
	legacy, stopped, _ := fake.calls()
	if legacy != 0 || stopped != 0 {
		t.Fatalf("legacy=%d stopped=%d, want no stop of any kind", legacy, stopped)
	}
	// The fake is still serving: this busy fixture was in fact left alive,
	// but the client-side contract only reports the ambiguous deadline.
	if _, err := client.GetConfig(context.Background(), &proto.GetConfigRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("unrelated probe after busy wait: %v", err)
	}
}

// Regression: the server may COMMIT the stop just before the deadline and
// lose the confirmation in transit. A DeadlineExceeded response therefore
// carries no proof that the agent was busy or left alive — the client must
// report the typed uncertain outcome even though the fake records that the
// stop was actually committed.
func TestSafeStopRequestDeadlineAfterCommitIsUncertainNotBusy(t *testing.T) {
	fake := &safeStopFake{agentPID: 1234, commitThenHold: true}
	client := startSafeStopFake(t, fake)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := safeStopRequest(ctx, client, 1234, "Homebrew installation updated")
	if !isSafeStopUncertain(err) {
		t.Fatalf("committed-then-deadline: err=%v, want typed uncertain diagnostic", err)
	}
	if isSafeStopUnsupported(err) {
		t.Fatalf("committed-then-deadline misclassified as unsupported: %v", err)
	}
	legacy, stopped, stopPID := fake.calls()
	if legacy != 0 {
		t.Fatalf("legacy=%d, want zero legacy calls", legacy)
	}
	// The stop WAS committed server-side before the deadline: proof that a
	// client-side deadline alone cannot claim "busy / left alive".
	if stopped != 1 || stopPID != 1234 {
		t.Fatalf("stopped=%d stopPID=%d, want the stop committed for pid 1234", stopped, stopPID)
	}
}

// A transport drop (Unavailable) is equally ambiguous: the stop may have
// committed before the connection was lost. The typed uncertain outcome must
// fire, and nothing may be claimed about the agent being busy or alive.
func TestSafeStopRequestUnavailableTransportIsUncertain(t *testing.T) {
	fake := &safeStopFake{agentPID: 1234, unavailable: true}
	client := startSafeStopFake(t, fake)

	err := safeStopRequest(context.Background(), client, 1234, "Homebrew installation updated")
	if !isSafeStopUncertain(err) {
		t.Fatalf("unavailable transport: err=%v, want typed uncertain diagnostic", err)
	}
	legacy, _, _ := fake.calls()
	if legacy != 0 {
		t.Fatalf("legacy=%d, want zero legacy calls", legacy)
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
// kind, no uncertain/unsupported misclassification, no legacy fallback.
func TestSafeStopRequestWrongPIDIsRefusedWithoutAnyStop(t *testing.T) {
	fake := &safeStopFake{agentPID: 100}
	client := startSafeStopFake(t, fake)

	err := safeStopRequest(context.Background(), client, 999, "Homebrew installation updated")
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("wrong pid: err=%v, want FailedPrecondition", err)
	}
	if isSafeStopUncertain(err) || isSafeStopUnsupported(err) {
		t.Fatalf("wrong pid misclassified as uncertain/unsupported: %v", err)
	}
	legacy, stopped, _ := fake.calls()
	if legacy != 0 || stopped != 0 {
		t.Fatalf("legacy=%d stopped=%d, want no stop after PID refusal", legacy, stopped)
	}
}

// TestSafeStopRequestNoAgent documents the RPC layer's contribution to the
// no-agent contract: with no server reachable, a lazy client cannot
// distinguish "never reached" from "committed then lost in transit", so the
// transport ending is typed UNCERTAIN — never a definitive busy/unsupported
// skip claim. (The coordinator never reaches this path for a truly absent
// agent: discovery returns before any stop RPC, and the blocking dial plus
// listener-ownership recheck precede the safe stop.)
func TestSafeStopRequestUnreachableAgentIsUncertainNotDefinitive(t *testing.T) {
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
	if !isSafeStopUncertain(err) {
		t.Fatalf("no agent: err=%v, want the typed uncertain transport outcome", err)
	}
	if isSafeStopUnsupported(err) {
		t.Fatalf("no agent: err=%v, must not be the definitive unsupported claim", err)
	}
}
