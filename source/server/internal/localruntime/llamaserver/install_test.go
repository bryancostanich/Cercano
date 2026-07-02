package llamaserver

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// withInstallCommand swaps installCommand for the test's duration.
func withInstallCommand(t *testing.T, f func(ctx context.Context) (*exec.Cmd, error)) {
	t.Helper()
	orig := installCommand
	installCommand = f
	t.Cleanup(func() { installCommand = orig })
}

// shScript returns a factory that runs the given shell one-liner. sh's
// combined stdout/stderr is what real brew produces, so this exercises the
// interleaved-stream path without depending on the host having brew
// installed.
func shScript(script string) func(ctx context.Context) (*exec.Cmd, error) {
	return func(ctx context.Context) (*exec.Cmd, error) {
		return exec.CommandContext(ctx, "sh", "-c", script), nil
	}
}

// collectLines returns a LineSink that appends to a slice under a mutex.
// Mutex is required because streamLines runs stdout and stderr concurrently.
func collectLines() (LineSink, *[]string, *sync.Mutex) {
	var mu sync.Mutex
	var out []string
	sink := func(line string) {
		mu.Lock()
		out = append(out, line)
		mu.Unlock()
	}
	return sink, &out, &mu
}

func TestInstall_StreamsStdoutLinesAndReturnsNilOnExit0(t *testing.T) {
	withInstallCommand(t, shScript("echo line-one; echo line-two; exit 0"))

	sink, lines, mu := collectLines()
	if err := Install(context.Background(), sink); err != nil {
		t.Fatalf("Install: unexpected error: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(*lines), *lines)
	}
	// Order across stdout+stderr isn't guaranteed by the pipe reader
	// goroutines, but within a single stream it is. Both these lines are on
	// stdout so they must come in order.
	if (*lines)[0] != "line-one" || (*lines)[1] != "line-two" {
		t.Errorf("stdout lines out of order: %v", *lines)
	}
}

func TestInstall_StreamsStderrLinesToo(t *testing.T) {
	// brew's status output goes to stderr; the sink must see those too.
	withInstallCommand(t, shScript("echo 'downloading...' >&2; echo done; exit 0"))

	sink, lines, mu := collectLines()
	if err := Install(context.Background(), sink); err != nil {
		t.Fatalf("Install: unexpected error: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(*lines, "\n")
	if !strings.Contains(joined, "downloading...") {
		t.Errorf("stderr line missing from sink; got: %v", *lines)
	}
	if !strings.Contains(joined, "done") {
		t.Errorf("stdout line missing from sink; got: %v", *lines)
	}
}

func TestInstall_ReturnsSubprocessErrorOnNonzeroExit(t *testing.T) {
	withInstallCommand(t, shScript("echo failing >&2; exit 3"))

	sink, lines, mu := collectLines()
	err := Install(context.Background(), sink)
	if err == nil {
		t.Fatal("Install should return an error on exit 3")
	}
	mu.Lock()
	defer mu.Unlock()
	// The failing stderr line was still streamed before the exit.
	if len(*lines) == 0 || !strings.Contains(strings.Join(*lines, ""), "failing") {
		t.Errorf("expected 'failing' line to reach sink before error, got: %v", *lines)
	}
}

func TestInstall_CancelledContextKillsSubprocessAndReturnsCtxErr(t *testing.T) {
	// A sleep-100 subprocess that we cancel immediately. Install should
	// return context.Canceled, not the "signal: killed" system error.
	withInstallCommand(t, shScript("sleep 100"))

	sink, _, _ := collectLines()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Install(ctx, sink)
	}()
	// Give the subprocess a beat to start before cancelling — otherwise
	// exec.CommandContext may not have installed the kill handler yet.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Install did not return after cancel")
	}
}

func TestInstall_UnsupportedPlatformReturnsErrUnsupported(t *testing.T) {
	withInstallCommand(t, func(ctx context.Context) (*exec.Cmd, error) {
		return nil, ErrUnsupported
	})
	err := Install(context.Background(), nil)
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("expected ErrUnsupported, got %v", err)
	}
}

func TestInstall_NilSinkDoesNotPanic(t *testing.T) {
	// Callers can pass nil when they only care about the exit status.
	// A brief succeeding script confirms Install substitutes a no-op sink
	// and doesn't segfault on the line-write path.
	withInstallCommand(t, shScript("echo hi; exit 0"))
	if err := Install(context.Background(), nil); err != nil {
		t.Errorf("Install(nil sink) errored: %v", err)
	}
}
