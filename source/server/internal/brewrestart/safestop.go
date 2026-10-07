package brewrestart

import (
	"context"
	"errors"
	"fmt"

	"cercano/source/server/pkg/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Safe-stop outcomes for the update-related stop of a running agent. They
// distinguish "the stop's outcome is unconfirmed" (a skip-with-guidance state
// that is not a failure of the package update) from a failed restart, so a
// skipped restart is never reported as a failed package update.
var (
	// ErrSafeStopUncertain: the safe-stop RPC ended in deadline, transport or
	// availability ambiguity. The agent may have committed the stop just
	// before the deadline (with the confirmation lost in transit) or may
	// still be running with update-relevant work in flight. A client-side
	// deadline alone NEVER proves the agent was busy or left alive, so this
	// outcome never claims "left running" on its own; the coordinator may
	// refine it with a fresh bounded local inspection of the verified
	// identity, and the CLI reports the unconfirmed state.
	ErrSafeStopUncertain = errors.New("safe stop outcome is unconfirmed")
	// ErrSafeStopUnsupported: the running agent predates the safe-stop RPC
	// and answered codes.Unimplemented. It was left running; the legacy
	// fire-and-forget ShutdownAgent bounce is NEVER used as a fallback.
	ErrSafeStopUnsupported = errors.New("running agent predates safe stop")
)

func isSafeStopUncertain(err error) bool   { return errors.Is(err, ErrSafeStopUncertain) }
func isSafeStopUnsupported(err error) bool { return errors.Is(err, ErrSafeStopUnsupported) }

// IsSafeStopUncertain reports that the safe-stop RPC ended ambiguously
// (deadline, transport or availability). The agent may already have stopped
// or may still be running; nothing was force-stopped and no replacement was
// started. It is a skip-with-guidance outcome, not a failure of the package
// update itself: the caller must not claim the agent was busy or left alive
// from the ambiguous deadline alone.
func IsSafeStopUncertain(err error) bool { return isSafeStopUncertain(err) }

// IsSafeStopUnsupported reports that the running agent predates the
// safe-stop RPC and was deliberately left running (nothing stopped, nothing
// started, no legacy fallback). It is a skip-with-guidance outcome, not a
// failure of the package update itself.
func IsSafeStopUnsupported(err error) bool { return isSafeStopUnsupported(err) }

// safeStopRequest issues the update-related stop to a VERIFIED agent: the
// expected PID comes from the coordinator's kernel-verified ownership
// inspection, never from a client-supplied or guessed value. The RPC is
// bounded by ctx and blocks until the agent observes its update-relevant
// work idle.
//
// An expired deadline (or a dropped connection) does NOT prove the agent was
// busy or left alive: the server may have committed the stop just before the
// deadline with the confirmation lost in transit. Those endings therefore
// return the typed uncertain outcome; only an explicit answer from the agent
// (accept, refusal, or Unimplemented for pre-safe-stop binaries) is treated
// as definitive.
func safeStopRequest(ctx context.Context, agent proto.AgentClient, expectedPID int, reason string) error {
	resp, err := agent.ShutdownAgentWhenIdle(ctx, &proto.ShutdownAgentWhenIdleRequest{
		ExpectedPid: int64(expectedPID),
		Reason:      reason,
	})
	switch {
	case err == nil:
		if !resp.GetAccepted() {
			return fmt.Errorf("agent refused safe stop: %s", resp.GetMessage())
		}
		return nil
	case status.Code(err) == codes.Unimplemented:
		return fmt.Errorf("%w: %v", ErrSafeStopUnsupported, err)
	case status.Code(err) == codes.DeadlineExceeded,
		status.Code(err) == codes.Unavailable,
		status.Code(err) == codes.Canceled:
		return fmt.Errorf("%w: %v", ErrSafeStopUncertain, err)
	default:
		return err
	}
}
