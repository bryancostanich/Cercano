// Package launch is the narrow independent-process primitive for the
// approved one-shot update utility (spec.md, "Approved simplification:
// one-shot utility, not a service").
//
// Contract and deliberate non-goals:
//
//   - Options.Executable is an EXPLICIT, already-resolved, trusted absolute
//     path and Options.Argv is a FIXED vector built entirely by compiled
//     caller code. This primitive never searches PATH, the user's home
//     directory or installation defaults, never consults update metadata
//     for commands or arguments, and never invokes a shell. The production
//     caller MUST resolve and verify the executable and its installation
//     BEFORE calling.
//
//   - The child must survive the initiating parent's exit and must not
//     inherit blocking stdin/console or parent-owned process-group/job
//     termination. On Unix the child starts in its own session (setsid,
//     the same detach pattern as the existing agentclient launch). On
//     Windows it starts with deliberate creation flags (own process group,
//     detached console). CREATE_BREAKAWAY_FROM_JOB is deliberately NOT
//     set: escaping a restrictive job object is neither claimed nor
//     attempted (see launch_windows.go).
//
//   - Progress is not control: child stdio is never a parent-owned pipe.
//     stdin is always the platform null device; stdout/stderr go to
//     caller-owned regular log files or are discarded. Output setup
//     failures are reported, never silently swallowed, and a closed or
//     disconnected UI can neither observe nor terminate the child through
//     stdio.
//
//   - The launcher never waits for, cancels, signals or supervises the
//     child, and it acquires NO lock: the launched utility is expected to
//     acquire its own update exclusion lease (updatecoord/exclusion); a
//     parent must never hold a lease its child then needs.
//
//   - This is a launch primitive only. It copies and replaces no installed
//     files, performs no updates, stops no agents, and adds no service,
//     listener, control credential or new binary entrypoint.
package launch

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

var (
	// ErrInvalidExecutable: Options.Executable is empty, relative, missing
	// or a directory. This fails closed on purpose: a relative or
	// unresolvable path must never silently reintroduce PATH, home or
	// default-binary resolution.
	ErrInvalidExecutable = errors.New("launch: executable must be an existing absolute file path")
	// ErrUnsupportedPlatform: this platform has no implemented
	// independent-process launch. Fail closed rather than starting a
	// child that shares the parent's process group/console and would be
	// terminated with it.
	ErrUnsupportedPlatform = errors.New("launch: no independent-process launch on this platform")
	// ErrOutputSetup: a caller-supplied output log path could not be
	// opened. The child is never started with half-wired or blocking
	// output.
	ErrOutputSetup = errors.New("launch: output log could not be opened")
)

// Options is the complete, caller-built description of one independent
// launch. Nothing here is resolved, defaulted, or taken from release or
// update metadata.
type Options struct {
	// Executable is the REQUIRED absolute path of an already-resolved,
	// verified, trusted executable. The production caller verifies it and
	// its installation beforehand; this primitive only fails closed on
	// obviously unusable paths.
	Executable string

	// Argv is the fixed argument vector passed to Executable. Caller-built
	// only; it may be empty.
	Argv []string

	// Dir is an optional working directory; empty means inherit the
	// parent's.
	Dir string

	// Env is the complete child environment; nil inherits the parent's.
	Env []string

	// StdoutPath and StderrPath are optional caller-owned REGULAR log file
	// paths (created if missing, always appended). Empty discards the
	// stream. They are never pipes and never handles whose lifecycle the
	// parent's exit or stdio closure could interrupt.
	StdoutPath string
	StderrPath string
}

// Process identifies one launched independent child. The launcher never
// waits; Pid exists only for logging and caller-side coordination (for
// example, test fixtures or an operator-facing status display).
type Process struct{ pid int }

// Pid returns the launched child's process id.
func (p *Process) Pid() int { return p.pid }

// Launch starts opts.Executable as an independent process and returns
// immediately.
//
// The child survives the initiating parent's exit, inherits no blocking
// stdin, and writes output only to caller-owned regular log files or to the
// null device. See the package doc for the full contract.
//
// Fail closed: a relative or missing executable, an unsupported platform,
// or an unopenable output log returns an error with no child started.
func Launch(opts Options) (*Process, error) {
	if opts.Executable == "" {
		return nil, fmt.Errorf("%w: empty path", ErrInvalidExecutable)
	}
	if !filepath.IsAbs(opts.Executable) {
		return nil, fmt.Errorf("%w: %q is a relative path", ErrInvalidExecutable, opts.Executable)
	}
	info, err := os.Stat(opts.Executable)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidExecutable, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%w: %q is a directory", ErrInvalidExecutable, opts.Executable)
	}

	// Platform independence settings. Unsupported platforms fail closed
	// rather than launching a child that shares the parent's process
	// group/console and would be terminated with it.
	attr, err := detachedSysProcAttr()
	if err != nil {
		return nil, err
	}

	// stdin is never the parent's terminal or a parent-held pipe: always
	// the platform null device, so the child can never block on or be
	// terminated by inherited stdin.
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("launch: opening %s: %w", os.DevNull, err)
	}
	defer null.Close() //nolint:errcheck // parent copy only; the child has its own descriptor

	stdout, err := openOutputStream(opts.StdoutPath)
	if err != nil {
		return nil, err
	}
	defer stdout.Close() //nolint:errcheck // parent copy only
	stderr, err := openOutputStream(opts.StderrPath)
	if err != nil {
		return nil, err
	}
	defer stderr.Close() //nolint:errcheck // parent copy only

	cmd := exec.Command(opts.Executable, opts.Argv...)
	cmd.Dir = opts.Dir
	cmd.Env = opts.Env
	cmd.Stdin = null
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = attr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("launch: starting %s: %w", opts.Executable, err)
	}
	pid := cmd.Process.Pid
	// The launcher never waits for the child, so release the parent-side
	// handle: the runtime stops tracking a child this process will not
	// reap. The pid stays valid for the caller's coordination purposes.
	// When the initiating parent later exits, the kernel reparents the
	// child (init/launchd on Unix; the process object outlives handles on
	// Windows).
	_ = cmd.Process.Release()
	return &Process{pid: pid}, nil
}

// openOutputStream opens one child output stream: a caller-owned regular
// log file (created, append-only) when path is non-empty, otherwise the
// platform null device (discard). Never a pipe: the child must never
// depend on the parent reading anything, and the parent must never depend
// on the child's output for progress.
func openOutputStream(path string) (*os.File, error) {
	if path == "" {
		return os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrOutputSetup, path, err)
	}
	return f, nil
}
