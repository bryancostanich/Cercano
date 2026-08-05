// Package llamaserver — install.go: managed install of llama-server via the
// platform's package manager, with line-by-line output streaming.
//
// The user-facing CLI hangs a modal off this: the server forks brew (or the
// platform equivalent), each stdout/stderr line is fed to the caller's sink,
// and the caller ships those lines over its gRPC stream to a scrollable log
// pane. Cancelation-via-context kills the subprocess (so a "Cancel" click in
// the modal takes effect immediately, not "after the current step").
//
// Platform coverage: macOS via Homebrew, Windows via winget (when present).
// Everywhere else — Windows without winget, and Linux, which has no
// consistent cross-distro package for llama.cpp — Install returns a
// descriptive ErrUnsupported-wrapped error naming the release page and the
// PATH requirement, so callers can render it directly rather than launching
// something that will fail cryptically.
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
// message with a URL, not retry. Wrapped with platform-specific guidance by
// defaultInstallCommand — check with errors.Is, not string equality.
var ErrUnsupported = errors.New("managed install is not supported on this platform")

// llamaCppReleasesURL is where a user lands to grab a prebuilt llama-server
// when no managed install path applies on their platform.
const llamaCppReleasesURL = "https://github.com/ggml-org/llama.cpp/releases"

// wingetPackageID is the winget-pkgs manifest ID that ships llama-server.exe
// (github.com/microsoft/winget-pkgs/tree/master/manifests/g/ggml/llamacpp).
const wingetPackageID = "ggml.llamacpp"

// wingetInstallArgs is shared by the real installCommand and by
// DetectError.SuggestedCommand (detect.go) so the suggestion text never
// drifts from the command "Install now" actually runs.
func wingetInstallArgs() []string {
	return []string{"install", "--id", wingetPackageID, "-e",
		"--silent", "--accept-package-agreements", "--accept-source-agreements"}
}

// wingetAlreadySatisfiedExitCodes are winget's own exit codes for "the
// package is already in the desired state" — documented in winget-cli's
// doc/windows/package-manager/winget/returnCodes.md. A plain `winget install`
// (not `upgrade`) against an already-present package still exits nonzero
// with one of these even though there's nothing left to do: the binary the
// caller wants is already on disk. Treated as success, not an install
// failure — retrying would just hit the same "error" forever.
var wingetAlreadySatisfiedExitCodes = map[int]bool{
	0x8A15002B: true, // APPINSTALLER_CLI_ERROR_UPDATE_NOT_APPLICABLE: "No applicable update found"
	0x8A150061: true, // APPINSTALLER_CLI_ERROR_PACKAGE_ALREADY_INSTALLED: "Found at least one version of the package installed."
}

// installAlreadySatisfied reports whether a nonzero subprocess exit actually
// means "already installed, nothing to do" rather than a real failure. Only
// meaningful for winget on Windows — no other installCommand branch shares
// these exit codes, so it's unconditionally false elsewhere.
func installAlreadySatisfied(err error) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return wingetAlreadySatisfiedExitCodes[exitErr.ExitCode()]
}

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
		if !installAlreadySatisfied(err) {
			return err
		}
		// Fall through: winget exited nonzero, but only because the package
		// is already present — that's success from the caller's POV.
	}
	// The install just succeeded (or was already satisfied) — on Windows, winget persists PATH to the
	// registry and broadcasts WM_SETTINGCHANGE, but this already-running
	// process never receives that broadcast, so its in-memory PATH is stale
	// until refreshed. Without this, the Detect the caller runs immediately
	// after Install would fail to find the freshly-installed binary even
	// though winget reported success. No-op on platforms where install
	// doesn't touch PATH out from under a running process (darwin's brew
	// symlinks into an already-on-PATH directory; other platforms have no
	// managed install at all).
	refreshPATH()
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

// refreshPATH is a var (like installCommand) so tests can stub it out.
// defaultRefreshPATH is platform-specific: install_windows.go re-reads the
// registry-persisted PATH into this process's environment; install_refresh_other.go
// is a no-op everywhere else.
var refreshPATH = defaultRefreshPATH

// defaultInstallCommand builds the exec.Cmd for the current platform.
// Returns an ErrUnsupported-wrapped error naming the manual-install fallback
// when no managed install path is defined (or applies); callers translate
// that into a "please install manually" UX rather than a scary generic error.
func defaultInstallCommand(ctx context.Context) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		// Homebrew is the upstream distribution channel for llama.cpp on
		// macOS. brew install streams progress to stderr as "==> ..." lines
		// which read cleanly in a log pane.
		return exec.CommandContext(ctx, "brew", "install", "llama.cpp"), nil
	case "windows":
		// winget-pkgs carries an official ggml.llamacpp manifest that ships
		// llama-server.exe. Not every Windows machine has winget (it ships
		// with Windows 11 / recent Windows 10, but can be missing on older
		// or locked-down installs), so only offer it when it's actually on
		// PATH — otherwise fall through to the manual-install message.
		if _, err := exec.LookPath("winget"); err == nil {
			return exec.CommandContext(ctx, "winget", wingetInstallArgs()...), nil
		}
		return nil, fmt.Errorf("%w: winget not found — install winget (Microsoft Store \"App Installer\"), or install llama-server manually from %s and add it to your PATH", ErrUnsupported, llamaCppReleasesURL)
	default:
		// No consistent cross-distro package for llama.cpp (Homebrew/
		// Linuxbrew is inconsistently available; distro repos don't carry
		// it), so there's no managed path to try here.
		return nil, fmt.Errorf("%w: install llama-server manually from %s (or via your distro's package manager, if it has one) and ensure it's on your PATH", ErrUnsupported, llamaCppReleasesURL)
	}
}
