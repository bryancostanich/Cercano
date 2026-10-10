package launch

import (
	"context"
	"errors"
)

var ErrCompletionUnknown = errors.New("launch: owned child completion is unproven")

// Completion is a process-local receipt from the sole reaper. It cannot be
// reconstructed from a PID, serialized across a utility restart, or inferred
// from a timeout. It proves only this child terminated, not its descendants,
// image authenticity, health, or its association with any update operation.
// Recovery must bind the launch handle to the operation before using it.
type Completion struct{ process *Process }

// WaitForCompletion does not cancel, signal or detach the child. A canceled
// waiter gets no receipt; another waiter may subsequently observe completion.
// Nonzero exits (including signals) are completion, not successful health.
func (p *Process) WaitForCompletion(ctx context.Context) (Completion, error) {
	if ctx == nil || p == nil || p.done == nil || p.pid <= 0 {
		return Completion{}, ErrCompletionUnknown
	}
	if err := ctx.Err(); err != nil {
		return Completion{}, err
	}
	select {
	case <-ctx.Done():
		return Completion{}, ctx.Err()
	case <-p.done:
	}
	if !p.completed {
		return Completion{}, ErrCompletionUnknown
	}
	return Completion{process: p}, nil
}

// For requires the same owned launch handle, never just an equal PID or path.
func (c Completion) For(p *Process) bool {
	if p == nil || c.process != p || p.done == nil {
		return false
	}
	select {
	case <-p.done:
		return p.completed
	default:
		return false
	}
}
func (c Completion) ExitCode() (int, bool) {
	if !c.For(c.process) {
		return 0, false
	}
	return c.process.exitCode, true
}
