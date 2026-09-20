package builtins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"cercano/source/server/internal/capabilities"
)

// runCommandCap is the generic shell-out escape hatch. Stdout + stderr are
// captured (NOT streamed) so they can be size-bounded before hitting the
// model context. Each stream is capped at 16 KiB independently.
type runCommandCap struct{}

// RunCommand constructs the run_command capability (display name "Bash").
func RunCommand() capabilities.Capability { return runCommandCap{} }

func (runCommandCap) Name() string                  { return "run_command" }
func (runCommandCap) Tier() capabilities.Tier        { return capabilities.TierW }
func (runCommandCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent | capabilities.SurfaceMCP }
func (runCommandCap) Description() string {
	return "Run a command and capture its output. cmd is an argv array: the first element is the executable (name or path — paths with spaces are used as-is, never split) and the remaining elements are its arguments. There is no implicit shell: no word splitting, quoting, pipes, or operators. Example: [\"ls\", \"/some/path\"]. To run shell syntax, invoke a shell explicitly, e.g. [\"bash\", \"-lc\", \"pwd && ls ..\"]. A whole command as one string (e.g. [\"ls /some/path\"]) or a list of separate commands is NOT interpreted and will fail. Other args: {cwd?: string, timeout_seconds?: int (omit for the 60s default; -1 runs with no timeout), env?: {key: value}}. Use -1 for genuinely unbounded work (long builds, migrations); the command is still killed if the turn is cancelled."
}
func (runCommandCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["cmd"],
		"properties": {
			"cmd":             {"type": "array", "items": {"type": "string"}, "minItems": 1,
			                    "description": "argv array: first element is the executable (name or path; a path with spaces is used as-is, never split), the rest are its arguments. No implicit shell — no splitting, quoting, pipes, or operators. Run a program directly: [\"ls\", \"/some/path\"]. Run shell syntax explicitly: [\"bash\", \"-lc\", \"pwd && ls ..\"]. A whole command in one string or a list of separate commands is not interpreted and will fail."},
			"cwd":             {"type": "string"},
			"timeout_seconds": {"type": "integer", "minimum": -1, "default": 60,
			                    "description": "Seconds before the command is killed. Omit or 0 for the 60s default. -1 disables the timeout entirely."},
			"env":             {"type": "object", "additionalProperties": {"type": "string"}}
		}
	}`)
}

type runCommandArgs struct {
	Cmd            []string          `json:"cmd"`
	Cwd            string            `json:"cwd"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	Env            map[string]string `json:"env"`
}

func (runCommandCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a runCommandArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("run_command: parse args: %w", err)
	}
	if len(a.Cmd) == 0 {
		return nil, errors.New("run_command: cmd is required and must have at least one element")
	}

	// Timeout semantics:
	//   omitted / 0 -> default (Go's zero value makes "absent" and "explicit
	//                  0" indistinguishable, which is exactly why the
	//                  unbounded sentinel is -1 and not 0)
	//   > 0         -> that many seconds
	//   -1          -> no timeout; run to completion
	//   < -1        -> rejected, so a typo'd -60 can't silently mean "forever"
	timeout, err := runCmdResolveTimeout(a.TimeoutSeconds)
	if err != nil {
		return nil, err
	}

	// Unbounded still derives from ctx: no deadline, but turn cancellation
	// (Esc) and shutdown continue to reap the process group.
	var runCtx context.Context
	var cancel context.CancelFunc
	if timeout == noRunTimeout {
		runCtx, cancel = context.WithCancel(ctx)
	} else {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, a.Cmd[0], a.Cmd[1:]...)
	// Own process group + group-wide SIGTERM on cancel, so a timeout reaps
	// backgrounded grandchildren instead of just the direct child.
	setRunProcessGroup(cmd)
	cmd.Cancel = func() error { return terminateRunGroup(cmd.Process) }
	// Bound the post-cancel wait. This is the load-bearing part: a grandchild
	// that inherited the stdout/stderr pipe keeps the write end open, so
	// without WaitDelay, Wait blocks until that grandchild exits — the
	// timeout would be silently ignored (observed: 60s elapsed on a 2s cap).
	cmd.WaitDelay = runCommandWaitDelay
	dir := a.Cwd
	if dir == "" {
		dir = call.WorkDir
	}
	if dir != "" {
		cmd.Dir = dir
	}
	if len(a.Env) > 0 {
		// Append to the agent's environment so the command sees PATH etc.
		cmd.Env = runCmdEnvOf(a.Env)
	}

	// syncBuffer, not bytes.Buffer: when WaitDelay fires, os/exec abandons the
	// output-copying goroutines and returns while they may still be writing.
	// Reading a plain buffer here would be a data race.
	var stdout, stderr syncBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startedAt := time.Now()
	err = cmd.Run()
	elapsed := time.Since(startedAt)

	// Final sweep: WaitDelay's escalation only SIGKILLs the direct child, so
	// anything left in the group after a timeout is killed here. Harmless on
	// the success path — by then the group is empty and the signal is ESRCH.
	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)
	if timedOut && cmd.Process != nil {
		killRunGroup(cmd.Process.Pid)
	}

	exitCode := 0
	if err != nil {
		// Check context deadline FIRST — exec.CommandContext kills the child
		// on timeout, which surfaces as a non-nil ExitError. Without this
		// guard the ExitError branch would swallow the timeout.
		if timedOut {
			// Surface whatever the command managed to print before it was
			// killed: on a hang, that partial output is usually the only
			// evidence of where it got stuck.
			return nil, fmt.Errorf("run_command: timed out after %s%s", timeout,
				runCmdPartialOutput(stdout.Bytes(), stderr.Bytes()))
		}
		// Turn cancelled (Esc) or server shutdown. Worth distinguishing from a
		// plain non-zero exit: with timeout_seconds=-1 this is the *only* way
		// a long run ends early, so it should not look like the command
		// itself failed.
		if errors.Is(runCtx.Err(), context.Canceled) {
			return nil, fmt.Errorf("run_command: cancelled after %s%s", elapsed.Round(time.Millisecond),
				runCmdPartialOutput(stdout.Bytes(), stderr.Bytes()))
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else if runCmdExecutableMissing(err) {
			// Executable lookup/start failed because the executable could not
			// be found. The dominant cause is contract misuse — a whole
			// command or a list of commands where the executable belongs —
			// so keep the original error and append the argv contract.
			return nil, fmt.Errorf("run_command: %w%s", err, runCmdArgvHint)
		} else {
			// Other start failures (permissions, I/O, ...) are not lookup
			// errors; do not mislabel them with the argv hint.
			return nil, fmt.Errorf("run_command: %w", err)
		}
	}

	// Truncate each stream independently — caps at 16 KiB each so a single
	// chatty side doesn't eat the entire budget. NewTextResult later may
	// apply the joint 32 KiB cap.
	const perStreamCap = 16 * 1024
	soStr := runCmdTruncateBytes(stdout.Bytes(), perStreamCap)
	seStr := runCmdTruncateBytes(stderr.Bytes(), perStreamCap)

	body := fmt.Sprintf("$ %s\n\n[exit=%d, elapsed=%s]\n", runCmdJoinShell(a.Cmd), exitCode, elapsed.Round(time.Millisecond))
	if soStr != "" {
		body += "\nstdout:\n" + soStr
	}
	if seStr != "" {
		body += "\nstderr:\n" + seStr
	}

	res := capabilities.NewTextResult(body)
	res.Detail = fmt.Sprintf("exit %d", exitCode)

	// One-line glance summary for the folded scrollback entry. Prepend rather
	// than overwrite so a truncation note from NewTextResult survives.
	summary := fmt.Sprintf("exit %d · %s", exitCode, elapsed.Round(time.Millisecond))
	if res.Note == "" {
		res.Note = summary
	} else {
		res.Note = summary + " · " + res.Note
	}

	return res, nil
}

// runCmdArgvHint is appended to executable-not-found errors. It explains the
// argv contract because the dominant cause of such errors is a whole command
// (or a list of commands) passed where the executable belongs. Pure text —
// never wrapped, so the original error stays the primary payload.
const runCmdArgvHint = `
cmd is an argv array, not a shell line: the first element is the executable (name or path) and the remaining elements are its arguments. There is no implicit shell — no splitting, quoting, or shell operators.
  - run a program directly:      ["ls", "/some/path"]
  - run shell syntax explicitly: ["bash", "-lc", "pwd && ls .."]
A whole command as one string (["ls /some/path"]) or a list of separate commands is not interpreted. If the executable truly should exist, check its name or path.`

// runCmdExecutableMissing reports whether err means "the executable could not
// be found" — a PATH lookup failure (exec.Error wrapping ErrNotFound) or a
// start failure for a missing file (fs.ErrNotExist). It deliberately excludes
// everything else: normal non-zero exits (ExitError), permission failures,
// and other start errors must not be mislabeled as lookup problems.
func runCmdExecutableMissing(err error) bool {
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return errors.Is(execErr.Err, exec.ErrNotFound) || errors.Is(execErr.Err, os.ErrNotExist)
	}
	return errors.Is(err, os.ErrNotExist)
}

// noRunTimeout is the sentinel Duration meaning "no deadline", produced by
// timeout_seconds = -1. Distinct from 0, which means "use the default".
const noRunTimeout = time.Duration(-1)

// runCmdDefaultTimeout is used when timeout_seconds is omitted or 0.
const runCmdDefaultTimeout = 60 * time.Second

// runCmdResolveTimeout maps the raw timeout_seconds argument onto a Duration.
// See the call site for the full semantics table.
func runCmdResolveTimeout(secs int) (time.Duration, error) {
	switch {
	case secs == 0:
		return runCmdDefaultTimeout, nil
	case secs > 0:
		return time.Duration(secs) * time.Second, nil
	case secs == -1:
		return noRunTimeout, nil
	default:
		// Only -1 is a valid negative. Anything else is far more likely a
		// typo or a sign error than a deliberate request, and silently
		// treating it as unbounded would turn a slip into a hung turn.
		return 0, fmt.Errorf("run_command: invalid timeout_seconds %d: use a positive number of seconds, 0 for the default (%s), or -1 for no timeout", secs, runCmdDefaultTimeout)
	}
}

// runCommandWaitDelay bounds how long os/exec waits, after cancellation, for
// the child's I/O pipes to close before abandoning them and returning. It only
// elapses on the timeout path; normal completion is unaffected. Variable so
// tests can shorten it.
var runCommandWaitDelay = 2 * time.Second

// syncBuffer is a bytes.Buffer safe for concurrent write-while-read. os/exec
// may still be copying child output when WaitDelay forces Wait to return.
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

// runCmdPartialOutput renders any output captured before a timeout, for
// appending to the timeout error. Returns "" when the command printed nothing.
func runCmdPartialOutput(stdout, stderr []byte) string {
	const partialCap = 4 * 1024
	out := ""
	if s := runCmdTruncateBytes(stdout, partialCap); s != "" {
		out += "\npartial stdout:\n" + s
	}
	if s := runCmdTruncateBytes(stderr, partialCap); s != "" {
		out += "\npartial stderr:\n" + s
	}
	return out
}

// runCmdEnvOf builds the "K=V" env slice, prepending the process's existing
// environment so PATH etc. survive.
func runCmdEnvOf(overrides map[string]string) []string {
	base := append([]string(nil), runCmdEnvBase()...)
	for k, v := range overrides {
		base = append(base, k+"="+v)
	}
	return base
}

// runCmdEnvBase returns os.Environ. Split out so tests can stub.
var runCmdEnvBase = func() []string { return os.Environ() }

// runCmdTruncateBytes caps b to maxBytes with a marker.
func runCmdTruncateBytes(b []byte, maxBytes int) string {
	if len(b) == 0 {
		return ""
	}
	if len(b) <= maxBytes {
		return string(b)
	}
	return string(b[:maxBytes]) + "\n…(truncated)"
}

// runCmdJoinShell renders argv as a copy-pasteable shell line.
func runCmdJoinShell(argv []string) string {
	out := ""
	for i, a := range argv {
		if i > 0 {
			out += " "
		}
		if runCmdContainsAny(a, " \t\n\"'$`") {
			out += "'" + a + "'"
		} else {
			out += a
		}
	}
	return out
}

func runCmdContainsAny(s, chars string) bool {
	for _, c := range chars {
		for _, r := range s {
			if r == c {
				return true
			}
		}
	}
	return false
}
