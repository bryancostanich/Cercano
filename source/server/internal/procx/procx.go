// Package procx runs external commands with a timeout that is actually
// enforced.
//
// The naive form — exec.CommandContext with a deadline — does not reliably
// bound wall time. exec.CommandContext kills only the direct child, so a
// command that backgrounds a grandchild leaves that grandchild holding the
// inherited stdout/stderr pipe write end, and Wait blocks draining a pipe
// that never closes. A 2s cap was observed taking 60s to return.
//
// procx.Run closes that hole for every caller:
//
//   - the child gets its own process group, so signals reach the whole tree;
//   - cancellation sends SIGTERM to the group, giving it a chance to clean up;
//   - WaitDelay bounds the post-cancel wait, so os/exec abandons a pipe a
//     grandchild is still holding instead of blocking on it;
//   - a final SIGKILL sweep reaps anything that ignored SIGTERM;
//   - partial output captured before the kill is returned, since on a hang
//     that output is usually the only evidence of where it got stuck.
//
// Timeouts are per-call and explicit. There is deliberately no package-wide
// default: "how long is too long" is a property of the work (a `git rev-parse`
// that takes 10s is broken; a test gate that takes 10s is fine), so each
// caller states its own bound. Use NoTimeout for genuinely unbounded work;
// cancellation via ctx still applies.
package procx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// NoTimeout disables the deadline for a call. The command still stops when
// ctx is cancelled — unbounded is not unstoppable.
const NoTimeout = time.Duration(-1)

// DefaultWaitDelay bounds how long os/exec waits, after cancellation, for the
// child's I/O pipes to close before abandoning them. Only elapses on the
// timeout/cancel path; normal completion is unaffected.
const DefaultWaitDelay = 2 * time.Second

var (
	// ErrTimeout is returned when the command exceeded Options.Timeout.
	ErrTimeout = errors.New("procx: command timed out")
	// ErrCanceled is returned when ctx was cancelled before the command
	// finished. Distinct from ErrTimeout so callers can tell "took too long"
	// from "the user pressed Esc".
	ErrCanceled = errors.New("procx: command canceled")
)

// Options describes one command invocation.
type Options struct {
	// Args is the argv; Args[0] is the executable. Required.
	Args []string
	// Dir is the working directory; empty means the current process's.
	Dir string
	// Env is the complete environment. Nil inherits the parent's.
	Env []string
	// Stdin, when non-empty, is written to the child's stdin.
	Stdin []byte
	// Timeout bounds wall time. Must be > 0, or NoTimeout to disable.
	// There is no zero-value default on purpose — see the package doc.
	Timeout time.Duration
	// WaitDelay overrides DefaultWaitDelay. Mainly for tests.
	WaitDelay time.Duration
}

// Result carries everything observed about the run, including on failure.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Elapsed  time.Duration
	// TimedOut and Canceled are also signalled via the returned error; they
	// are surfaced here too so callers that log Result need not unwrap.
	TimedOut bool
	Canceled bool
}

// Combined returns stdout and stderr concatenated, the common shape for
// error messages.
func (r Result) Combined() []byte {
	if len(r.Stderr) == 0 {
		return r.Stdout
	}
	if len(r.Stdout) == 0 {
		return r.Stderr
	}
	return append(append([]byte(nil), r.Stdout...), r.Stderr...)
}

// Run executes opts.Args, enforcing the timeout across the whole process
// group.
//
// A non-zero exit status is NOT an error: it is reported via Result.ExitCode
// with err == nil, because for many callers (git merge-base --is-ancestor,
// pgrep) a non-zero exit is a legitimate answer. Errors are reserved for
// "the command did not run to completion": spawn failure, timeout, or
// cancellation. Result is always populated, even alongside an error.
func Run(ctx context.Context, opts Options) (Result, error) {
	var res Result
	if len(opts.Args) == 0 {
		return res, errors.New("procx: Args is required")
	}
	if opts.Timeout <= 0 && opts.Timeout != NoTimeout {
		return res, fmt.Errorf("procx: Timeout must be > 0 or NoTimeout, got %v", opts.Timeout)
	}

	runCtx, cancel := withBound(ctx, opts.Timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, opts.Args[0], opts.Args[1:]...)
	cmd.Dir = opts.Dir
	cmd.Env = opts.Env

	setProcessGroup(cmd)
	cmd.Cancel = func() error { return terminateGroup(cmd.Process) }
	waitDelay := opts.WaitDelay
	if waitDelay <= 0 {
		waitDelay = DefaultWaitDelay
	}
	cmd.WaitDelay = waitDelay

	if len(opts.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(opts.Stdin)
	}

	// syncBuffer, not bytes.Buffer: when WaitDelay fires, os/exec abandons the
	// output-copying goroutines and returns while they may still be writing.
	var stdout, stderr syncBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startedAt := time.Now()
	err := cmd.Run()
	res.Elapsed = time.Since(startedAt)
	res.Stdout = stdout.Bytes()
	res.Stderr = stderr.Bytes()

	res.TimedOut = errors.Is(runCtx.Err(), context.DeadlineExceeded)
	res.Canceled = errors.Is(runCtx.Err(), context.Canceled)

	// Final sweep: WaitDelay's escalation only SIGKILLs the direct child, so
	// anything left in the group is killed here. Harmless on the success
	// path — by then the group is empty and the signal is ESRCH.
	if (res.TimedOut || res.Canceled) && cmd.Process != nil {
		killGroup(cmd.Process.Pid)
	}

	if err != nil {
		// Check ctx state BEFORE the ExitError branch: killing the child
		// surfaces as a non-nil ExitError, which would otherwise swallow the
		// timeout and report it as an ordinary failed command.
		switch {
		case res.TimedOut:
			return res, fmt.Errorf("%w after %v", ErrTimeout, opts.Timeout)
		case res.Canceled:
			return res, fmt.Errorf("%w after %v", ErrCanceled, res.Elapsed.Round(time.Millisecond))
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// Ran to completion, just unsuccessfully. Caller's business.
			res.ExitCode = ee.ExitCode()
			return res, nil
		}
		return res, fmt.Errorf("procx: %s: %w", opts.Args[0], err)
	}
	return res, nil
}

// withBound derives the run context: a deadline, or a plain cancellable child
// for NoTimeout so ctx cancellation still propagates.
func withBound(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout == NoTimeout {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

// syncBuffer is a bytes.Buffer safe for concurrent write-while-read, because
// os/exec may still be copying child output when WaitDelay forces Wait to
// return.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// Bytes returns a copy, so the caller never aliases memory a late-arriving
// write could mutate.
func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}
