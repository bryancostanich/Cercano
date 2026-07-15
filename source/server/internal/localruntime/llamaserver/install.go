// Package llamaserver — install.go: managed install of llama-server via the
// platform's package manager, with line-by-line output streaming.
//
// The user-facing CLI hangs a modal off this: the server forks brew (or the
// platform equivalent), each stdout/stderr line is fed to the caller's sink,
// and the caller ships those lines over its gRPC stream to a scrollable log
// pane. Cancelation-via-context kills the subprocess (so a "Cancel" click in
// the modal takes effect immediately, not "after the current step").
//
// Platform coverage today is macOS via Homebrew. Linux and Windows return
// ErrUnsupported so callers can render "Install llama.cpp manually and re-run
// detection" rather than launching something that will fail cryptically.
package llamaserver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"sync"
)

// ErrUnsupported means the current OS doesn't have a managed install path
// wired up. The caller should surface this as a "please install manually"
// message with a URL, not retry.
var ErrUnsupported = errors.New("managed install is not supported on this platform")

// LineSink is called once per line of subprocess output as it arrives. The
// line does NOT include a trailing newline. Implementations should be fast /
// non-blocking (channel send, gRPC stream write) — the subprocess reader
// blocks on the sink returning.
type LineSink func(line string)

// Install runs the platform install command for a llama-server runtime and
// streams its combined stdout/stderr to sink. Returns nil on subprocess
// exit-0, an error otherwise. A cancelled ctx kills the subprocess and
// returns ctx.Err().
//
// Streaming semantics: lines are delivered in-order across both stdout and
// stderr; the sink sees them as one interleaved stream (which matches what a
// user sees in a normal terminal invocation). No "STDERR:" prefix — brew's
// output uses stderr for status ("==> Downloading …"), and prefixing every
// such line would be noise.
func Install(ctx context.Context, sink LineSink) error {
	if sink == nil {
		sink = func(string) {}
	}
	cmd, err := installCommand(ctx)
	if err != nil {
		return err
	}
	// A cancelled ctx must kill the whole subprocess tree, not just the direct
	// child: a shell step that backgrounds a process leaves a grandchild that
	// keeps the stdout/stderr pipe's write end open, so the drain below would
	// never see EOF and Install would hang.
	configureCancelKillsGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start install: %w", err)
	}

	// One goroutine per pipe forwards lines to the sink. WaitGroup makes
	// sure both are drained before we call cmd.Wait — if we Wait too early
	// on a subprocess that's still flushing stdout, the pipe closes mid-line
	// and the final chunk is lost.
	var wg sync.WaitGroup
	wg.Add(2)
	go streamLines(&wg, stdout, sink)
	go streamLines(&wg, stderr, sink)
	wg.Wait()

	if err := cmd.Wait(); err != nil {
		// If the ctx was cancelled the subprocess was killed by exec — surface
		// the ctx err rather than the noisy "signal: killed" so the CLI sees
		// a coherent "cancelled" state.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

// streamLines reads r line-by-line, invoking sink for each. The scanner's
// default buffer is enough for brew output (short lines); if a runtime ever
// ships a step that produces a >64KB line, we'd need bufio.Scanner.Buffer.
func streamLines(wg *sync.WaitGroup, r io.Reader, sink LineSink) {
	defer wg.Done()
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		sink(sc.Text())
	}
}

// installCommand is a var (not a func) so tests can swap in a fake command
// factory that runs /bin/sh with scripted output instead of shelling out to
// real brew. The default factory is defaultInstallCommand.
var installCommand = defaultInstallCommand

// defaultInstallCommand builds the exec.Cmd for the current platform.
// Returns ErrUnsupported when no managed install path is defined; callers
// translate that into a "please install manually" UX rather than a scary
// generic error.
func defaultInstallCommand(ctx context.Context) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		// Homebrew is the upstream distribution channel for llama.cpp on
		// macOS. brew install streams progress to stderr as "==> ..." lines
		// which read cleanly in a log pane.
		return exec.CommandContext(ctx, "brew", "install", "llama.cpp"), nil
	default:
		return nil, ErrUnsupported
	}
}
