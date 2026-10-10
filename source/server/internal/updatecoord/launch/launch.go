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
//     Windows it starts with deliberate creation flags (own process
//     group, detached console, breakaway from the parent's job chain);
//     when the OS forbids the breakaway the launch is refused, never
//     silently downgraded to a child that shares the parent's job (see
//     launch_windows.go).
//
//   - Progress is not control: child stdio is never a parent-owned pipe.
//     stdin is always the platform null device; stdout/stderr go to
//     caller-owned regular log files or are discarded. Log paths must be
//     ABSOLUTE regular-file paths; relative paths, symlinks, FIFOs and
//     other non-regular files fail closed before any child starts, and
//     new log files are created 0600. This defends against
//     misconfiguration, not against a hostile same user: the parent
//     directory of every log path is assumed trusted and caller-owned.
//     Output setup failures are reported, never silently swallowed, and
//     a closed or disconnected UI can neither observe nor terminate the
//     child through stdio.
//
//   - The launcher never cancels, signals or supervises the child, and it
//     acquires NO lock: the launched utility is expected to acquire its
//     own update exclusion lease (updatecoord/exclusion); a parent must
//     never hold a lease its child then needs. The launcher's single
//     parent-side obligation is asynchronous reaping: an exited child
//     must not linger as a zombie while the initiating app runs. If the
//     parent exits first, the kernel reparents the child and the
//     platform init reaps it.
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
	// ErrInvalidExecutable: Options.Executable is empty, relative,
	// missing or not a regular file. This fails closed on purpose: a
	// relative, unresolvable or non-regular path must never silently
	// reintroduce PATH, home or default-binary resolution, and no child
	// may ever be exec'd from a device, FIFO or socket.
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
	// ErrInvalidOutputPath: StdoutPath/StderrPath was relative, or named
	// something other than a regular file (symlink, FIFO, device,
	// directory). The child is never started against it.
	ErrInvalidOutputPath = errors.New("launch: output log path must be absolute and name a regular file")
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

	// StdoutPath and StderrPath are optional caller-owned ABSOLUTE
	// regular-file log paths (created 0600 if missing, always appended).
	// Empty discards the stream. Relative paths, symlinks, FIFOs, devices
	// and any other non-regular file are refused. They are never pipes and
	// never handles whose lifecycle the parent's exit or stdio closure
	// could interrupt.
	StdoutPath string
	StderrPath string
}

// Process retains ownership of the launcher's single reaper. Pid is diagnostic
// only; it must never be used to signal or adopt a process after it is reaped.
type Process struct {
	pid        int
	executable string
	done       chan struct{}
	// Written only by the reaper, read only after done closes.
	completed bool
	exitCode  int
}

func (p *Process) Pid() int {
	if p == nil {
		return 0
	}
	return p.pid
}

// Executable is the immutable path supplied to this launch, not publisher proof.
func (p *Process) Executable() string {
	if p == nil {
		return ""
	}
	return p.executable
}

// Launch starts opts.Executable as an independent process and returns
// immediately.
//
// The child survives the initiating parent's exit, inherits no blocking
// stdin, and writes output only to caller-owned regular log files or to the
// null device. See the package doc for the full contract.
//
// Fail closed: a relative, missing or non-regular executable, an
// unsupported platform, or an invalid/unopenable output log returns an
// error with no child started.
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
	// Only a regular file may be executed: "not a directory" is not
	// enough, or a FIFO or device at the path would reach fork/exec.
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %q is not a regular file", ErrInvalidExecutable, opts.Executable)
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
	p := &Process{pid: cmd.Process.Pid, executable: opts.Executable, done: make(chan struct{}), exitCode: -1}
	// Only this goroutine calls Wait. It never signals or supervises. An exit
	// status error still proves termination; other wait errors do not.
	go func() {
		err := cmd.Wait()
		var exited *exec.ExitError
		p.completed = cmd.ProcessState != nil && (err == nil || errors.As(err, &exited))
		if p.completed {
			p.exitCode = cmd.ProcessState.ExitCode()
		}
		close(p.done)
	}()
	return p, nil
}

// openOutputStream opens one child output stream: a caller-owned regular
// log file (created 0600, append-only) when path is non-empty, otherwise
// the platform null device (discard). Never a pipe: the child must never
// depend on the parent reading anything, and the parent must never depend
// on the child's output for progress.
//
// Fail closed on anything but a regular file, in three layers: the path
// must be absolute; an existing path must already be a regular file
// (Lstat, so symlinks and FIFOs are rejected by type); and the opened
// descriptor itself must be regular, catching anything swapped in between
// the check and the open. openOutputFile adds platform non-follow and
// non-blocking open flags where available, so a swapped-in symlink or
// FIFO fails or returns instead of hanging the launch.
//
// These checks defend against misconfiguration and accidents, not
// against a hostile same user: the parent directory of the log path is
// assumed trusted and caller-owned, and no stronger same-user TOCTOU
// guarantee is claimed.
func openOutputStream(path string) (*os.File, error) {
	if path == "" {
		return os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: %q is a relative path", ErrInvalidOutputPath, path)
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%w: %q is not a regular file", ErrInvalidOutputPath, path)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s: %w", ErrOutputSetup, path, err)
	}
	f, err := openOutputFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrOutputSetup, path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%w: %s: %w", ErrOutputSetup, path, err)
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("%w: %q opened as a non-regular file", ErrInvalidOutputPath, path)
	}
	return f, nil
}
