package llamaserver

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strconv"
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

func TestInstall_CancelKillsSurvivingBackgroundChild(t *testing.T) {
	// The leader shell backgrounds a child that inherits the stdout/stderr
	// pipes, then blocks. exec.CommandContext's default cancel kills only the
	// leader (cmd.Process); the backgrounded child survives and keeps the pipe's
	// write end open, so Install's drain never sees EOF. Install must return
	// promptly with context.Canceled — which requires killing the whole process
	// group, not just the leader. Portable repro of the Linux CI hang (on macOS
	// a bare `sh -c "sleep N"` execs sleep in place, hiding the grandchild).
	withInstallCommand(t, shScript("sleep 30 & echo READY; sleep 30"))

	sink, _, _ := collectLines()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Install(ctx, sink)
	}()
	time.Sleep(100 * time.Millisecond) // let the shell start and background its child
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Install did not return after cancel (a surviving child held the output pipe open)")
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

// withRefreshPATH swaps refreshPATH for the test's duration, mirroring
// withInstallCommand above.
func withRefreshPATH(t *testing.T, f func()) {
	t.Helper()
	orig := refreshPATH
	refreshPATH = f
	t.Cleanup(func() { refreshPATH = orig })
}

func TestInstall_RefreshesPATHOnSuccessOnly(t *testing.T) {
	var calls int
	withRefreshPATH(t, func() { calls++ })

	withInstallCommand(t, shScript("exit 0"))
	if err := Install(context.Background(), nil); err != nil {
		t.Fatalf("Install: unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("refreshPATH called %d times on success, want 1", calls)
	}

	withInstallCommand(t, shScript("exit 1"))
	if err := Install(context.Background(), nil); err == nil {
		t.Fatal("Install should error on nonzero exit")
	}
	if calls != 1 {
		t.Errorf("refreshPATH called %d times after a failed install, want still 1 (no extra call)", calls)
	}
}

// TestInstallAlreadySatisfied_WingetKnownCodesTreatedAsSuccess exercises real
// subprocess exit codes rather than mocking exec.ExitError (which has no
// public constructor). POSIX sh truncates `exit N` to 8 bits, so it can't
// reproduce winget's >16-bit codes — cmd.exe can, which is also the only
// platform installAlreadySatisfied treats specially.
func TestInstallAlreadySatisfied_WingetKnownCodesTreatedAsSuccess(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("winget exit-code handling is windows-only")
	}
	for code := range wingetAlreadySatisfiedExitCodes {
		code := code
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			err := exec.Command("cmd", "/c", "exit", strconv.Itoa(code)).Run()
			if err == nil {
				t.Fatalf("expected process to exit nonzero for code %d", code)
			}
			if !installAlreadySatisfied(err) {
				t.Errorf("installAlreadySatisfied(%v) = false, want true for known winget code %d", err, code)
			}
		})
	}
}

func TestInstallAlreadySatisfied_UnknownNonzeroExitStillFails(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("winget exit-code handling is windows-only")
	}
	err := exec.Command("cmd", "/c", "exit", "1").Run()
	if err == nil {
		t.Fatal("expected process to exit nonzero")
	}
	if installAlreadySatisfied(err) {
		t.Error("installAlreadySatisfied should not treat an arbitrary exit 1 as already-satisfied")
	}
}

func TestInstall_TreatsWingetAlreadyInstalledAsSuccess(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("winget exit-code handling is windows-only")
	}
	var refreshed int
	withRefreshPATH(t, func() { refreshed++ })
	withInstallCommand(t, func(ctx context.Context) (*exec.Cmd, error) {
		// 2316632107 decimal == 0x8A15002B (APPINSTALLER_CLI_ERROR_UPDATE_NOT_APPLICABLE).
		// cmd.exe's `exit` builtin only parses decimal literals.
		return exec.CommandContext(ctx, "cmd", "/c", "echo already installed & exit 2316632107"), nil
	})
	sink, lines, mu := collectLines()
	if err := Install(context.Background(), sink); err != nil {
		t.Fatalf("Install should treat winget's already-installed exit as success, got: %v", err)
	}
	if refreshed != 1 {
		t.Errorf("refreshPATH called %d times, want 1", refreshed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*lines) == 0 {
		t.Error("expected the already-installed output to still reach the sink")
	}
}

func TestWingetInstallArgs(t *testing.T) {
	got := wingetInstallArgs()
	want := []string{"install", "--id", "ggml.llamacpp", "-e", "--silent", "--accept-package-agreements", "--accept-source-agreements"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("wingetInstallArgs = %v, want %v", got, want)
	}
}

func TestDefaultInstallCommand_WindowsUsesWingetWhenPresent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only: exercises the real winget branch of defaultInstallCommand")
	}
	if _, err := exec.LookPath("winget"); err != nil {
		t.Skip("winget not on this host's PATH")
	}
	cmd, err := defaultInstallCommand(context.Background())
	if err != nil {
		t.Fatalf("defaultInstallCommand: unexpected error: %v", err)
	}
	if !strings.Contains(cmd.Path, "winget") {
		t.Errorf("cmd.Path = %q, want it to resolve winget", cmd.Path)
	}
}

func TestDefaultInstallCommand_UnsupportedPlatformNamesManualInstallURL(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("darwin has a managed install path (brew)")
	}
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("winget"); err == nil {
			t.Skip("winget present on this host — managed install path applies")
		}
	}
	_, err := defaultInstallCommand(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
	if !strings.Contains(err.Error(), llamaCppReleasesURL) {
		t.Errorf("error should point at the manual-install release page, got: %v", err)
	}
}
