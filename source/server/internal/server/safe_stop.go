package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"cercano/source/server/pkg/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Safe stop: ShutdownAgentWhenIdle.
//
// This is the bounded, one-shot safe-stop entrypoint for updaters. It is
// deliberately DISTINCT from ShutdownAgent (which stays unchanged: an
// immediate, fire-and-forget restart bounce). Old agent binaries that predate
// this method answer it with codes.Unimplemented — the gRPC default for an
// unimplemented method on a server embedding UnimplementedAgentServer — and
// suffer no side effect, which is exactly why the method is new rather than a
// wait flag added to the old RPC.
//
// Sequence, with no effect until every guard has passed:
//
//  1. Request shape: nil request, non-positive expected_pid → InvalidArgument.
//  2. Identity guard: expected_pid must equal this process's PID, else
//     FailedPrecondition. This is an identity guard, NOT authentication: PID
//     equality never authenticates the caller and is not a security boundary.
//     It exists so a request composed against a previous agent process is
//     rejected instead of stopping the wrong agent. The external caller (the
//     updater/launcher) remains separately responsible for verifying the
//     actual process identity and installation before issuing the stop.
//  3. Idempotence: if a stop is already committed, return the same accepted
//     response — no duplicate commit, no second callback.
//  4. Configured stop path: a process-stop requester must be configured via
//     SetProcessStopRequester (the binary entrypoint wires the process-local
//     shutdown-request channel). Without it the request fails and NOTHING
//     happens — the handler never falls back to self-signaling and never
//     silently claims safety it cannot prove.
//  5. waitForUpdateIdle: wait for every update-relevant work source (turns,
//     runtime model downloads, compaction, resume hydration, credential
//     refresh) to be observed idle with full coverage proven. In-flight work
//     is NEVER cancelled. Cancelling the request ctx BEFORE the idle seal
//     releases it and leaves normal admission untouched.
//  6. Commit: atomically (under safeStopMu) mark committed and invoke the
//     configured stop requester ONCE. The admission seal is deliberately
//     NOT released on this path: new update-relevant work stays refused
//     until the process actually exits through its normal drain path. Only
//     the read-only observer RPCs (and the exempt administrative bounces)
//     remain served while the process drains.
//
// Commit boundary (the cancelled-request race): once pauseWhenIdle observes
// idle under the admission mutex, the seal belongs to this request even if its
// ctx is cancelled a moment later — the commit stands and the response may
// simply be lost to the cancelled stream; a repeated request observes
// "committed" and returns the idempotent accepted response without side
// effects. Before the seal, cancellation releases with no effect whatsoever.

// SetProcessStopRequester configures the process-stop path used by
// ShutdownAgentWhenIdle (and, when configured, scheduleSelfShutdown). The
// callback must initiate the process's normal drain-and-exit path — the same
// teardown the OS signal handler runs — and must NOT be a self-signal. The
// binary entrypoint passes its process-local shutdown-request channel sender;
// tests inject a fake so no test ever signals or kills its own host process.
// Passing nil removes the configured path (the safe-stop RPC then refuses).
func (s *Server) SetProcessStopRequester(fn func(reason string)) {
	s.safeStopMu.Lock()
	s.processStopRequester = fn
	s.safeStopMu.Unlock()
}

// processStopRequester returns the configured stop requester under the commit
// mutex, giving a happens-before edge to readers.
func (s *Server) getProcessStopRequester() func(reason string) {
	s.safeStopMu.Lock()
	defer s.safeStopMu.Unlock()
	return s.processStopRequester
}

// safeStopAlreadyCommitted reports whether a safe stop has committed.
func (s *Server) safeStopAlreadyCommitted() bool {
	s.safeStopMu.Lock()
	defer s.safeStopMu.Unlock()
	return s.safeStopCommitted
}

// safeStopResponse is the idempotent response every committed-path request
// returns, whether it committed the stop itself or observed an existing one.
func safeStopCommittedResponse() *proto.ShutdownAgentWhenIdleResponse {
	return &proto.ShutdownAgentWhenIdleResponse{
		Accepted: true,
		Message:  "safe stop committed: update-relevant work is idle; shutdown in progress",
	}
}

// ShutdownAgentWhenIdle implements proto.AgentServer — see the file-header
// comment for the full contract and commit boundary.
func (s *Server) ShutdownAgentWhenIdle(ctx context.Context, req *proto.ShutdownAgentWhenIdleRequest) (*proto.ShutdownAgentWhenIdleResponse, error) {
	// ── 1. Request shape, before any effect. ──
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "safe stop requires a request with a positive expected_pid")
	}
	if req.GetExpectedPid() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "safe stop requires a positive expected_pid")
	}

	// ── 2. Identity guard (NOT authentication), before any effect. ──
	if got, want := req.GetExpectedPid(), int64(os.Getpid()); got != want {
		return nil, status.Errorf(codes.FailedPrecondition,
			"expected_pid %d does not match this agent process (pid %d)", got, want)
	}

	// ── 3. Idempotent repeat: a committed stop needs no new work. ──
	if s.safeStopAlreadyCommitted() {
		return safeStopCommittedResponse(), nil
	}

	// ── 4. Configured stop path: refuse rather than guess. ──
	stop := s.getProcessStopRequester()
	if stop == nil {
		return nil, status.Error(codes.FailedPrecondition,
			"safe stop unavailable: this agent process has no configured stop path")
	}

	// ── 5. Wait for provably-idle, fully-tracked update-relevant work. ──
	release, err := s.waitForUpdateIdle(ctx)
	if err != nil {
		// A concurrent request may have committed while we waited (our wait
		// then observes the sealed gate); that is the idempotent outcome.
		if s.safeStopAlreadyCommitted() {
			return safeStopCommittedResponse(), nil
		}
		return nil, safeStopWaitError(err)
	}

	// ── 6. Atomic commit. The seal is intentionally kept (release dropped):
	// admission stays closed through the actual process stop; only read-only
	// observer RPCs (and the exempt administrative bounces) keep serving. ──
	s.safeStopMu.Lock()
	if s.safeStopCommitted {
		s.safeStopMu.Unlock()
		release() // defensive: a duplicate seal can only exist pre-commit; hand it back
		return safeStopCommittedResponse(), nil
	}
	s.safeStopCommitted = true
	s.safeStopMu.Unlock()

	reason := "safe stop committed: update-relevant work idle"
	if r := req.GetReason(); r != "" {
		reason = r
	}
	log.Printf("ShutdownAgentWhenIdle accepted: %s (pid %d)", reason, os.Getpid())

	// Invoke the configured stop requester exactly once. It initiates the
	// process's normal drain-and-exit path; we never self-signal here.
	stop(reason)

	return &proto.ShutdownAgentWhenIdleResponse{
		Accepted: true,
		Message:  "safe stop committed: update-relevant work is idle; shutdown initiated",
	}, nil
}

// safeStopWaitError maps a failed waitForUpdateIdle to a gRPC status. None of
// these paths has taken any effect: no commit, no callback, no held seal.
func safeStopWaitError(err error) error {
	switch {
	case errors.Is(err, errUpdateCoverageIncomplete):
		return status.Error(codes.FailedPrecondition, fmt.Sprintf("safe stop refused: %v", err))
	case errors.Is(err, errUpdateAdmissionPaused):
		return status.Error(codes.Aborted, fmt.Sprintf("safe stop refused: %v", err))
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "safe stop wait cancelled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "safe stop wait timed out")
	default:
		return status.Error(codes.Unavailable, fmt.Sprintf("safe stop wait failed: %v", err))
	}
}

// Server embeds the generated UnimplementedAgentServer base, so an agent
// binary predating this method answers ShutdownAgentWhenIdle with
// codes.Unimplemented — a plain gRPC status with no side effect — which is
// why the safe stop shipped as a distinct method rather than a wait flag on
// the old ShutdownAgent.
