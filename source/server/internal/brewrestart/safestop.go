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
// distinguish "deliberately skipped: the agent was left running" from a
// failed restart, so a skipped restart is never reported as a failed
// package update.
var (
	// ErrSafeStopBusy: the agent still had update-relevant work at the
	// bounded deadline. The safe-stop wait is never allowed to cancel work,
	// so the agent was left running and no replacement was started.
	ErrSafeStopBusy = errors.New("agent has active update-relevant work")
	// ErrSafeStopUnsupported: the running agent predates the safe-stop RPC
	// and answered codes.Unimplemented. It was left running; the legacy
	// fire-and-forget ShutdownAgent bounce is NEVER used as a fallback.
	ErrSafeStopUnsupported = errors.New("running agent predates safe stop")
)

func isSafeStopBusy(err error) bool        { return errors.Is(err, ErrSafeStopBusy) }
func isSafeStopUnsupported(err error) bool { return errors.Is(err, ErrSafeStopUnsupported) }

// IsSafeStopBusy reports that the agent had active update-relevant work at
// the bounded deadline and was deliberately left running (nothing stopped,
// nothing started). It is a skip-with-guidance outcome, not a failure of the
// package update itself.
func IsSafeStopBusy(err error) bool { return isSafeStopBusy(err) }

// IsSafeStopUnsupported reports that the running agent predates the
// safe-stop RPC and was deliberately left running (nothing stopped, nothing
// started, no legacy fallback). It is a skip-with-guidance outcome, not a
// failure of the package update itself.
func IsSafeStopUnsupported(err error) bool { return isSafeStopUnsupported(err) }

// safeStopRequest issues the update-related stop to a VERIFIED agent: the
// expected PID comes from the coordinator's kernel-verified ownership
// inspection, never from a client-supplied or guessed value. The RPC is
// bounded by ctx and blocks until the agent observes its update-relevant
// work idle, so an expired deadline always means the work was left running.
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
	case status.Code(err) == codes.DeadlineExceeded:
		return fmt.Errorf("%w: %v", ErrSafeStopBusy, err)
	default:
		return err
	}
}
