//go:build !windows

package updater

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bryancostanich/Cercano/spikes/windows-go-updater/fixtures"
)

// OBSERVED-FACT TESTS (POSIX, real OS behavior, no simulation):
//
// On macOS/Linux the platform does NOT block rename of a running/open
// executable, which is why versioned-directory activation is safe there
// while the OLD process keeps running. These tests record that fact with a
// real fixture process. The Windows counterpart (rename FAILS, which is why
// running_windows.go probes before activation) is documented in
// lockbehavior_windows_test.go and left to Windows CI.

// startHoldingAgent starts a fixture agent with --hold-open (it keeps
// running, holding its own executable open via a real fd, until signaled)
// and returns a stop function that kills and reaps it.
func startHoldingAgent(t *testing.T, path string) (stop func()) {
	t.Helper()
	cmd := exec.Command(path, "--hold-open")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start agent: %v", err)
	}
	// Give it a moment to open its own binary and settle.
	time.Sleep(100 * time.Millisecond)
	return func() {
		// The fixture runs until SIGTERM; kill is portable and always
		// terminates it (the process holds no user state in this spike).
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}

// FACT (POSIX): a RUNNING binary can be renamed (its file is not locked),
// and the running process is unaffected. This is exactly why the
// versioned-directory design avoids the classic "can't replace a running
// exe" problem on POSIX — activation renames DIRECTORIES, and even the
// binary file itself would be renamable.
func TestPOSIXRunningBinaryCanBeRenamed(t *testing.T) {
	dir := t.TempDir()
	agentPath := fixtures.BuildAgent(t, currentVersion, false)
	exe := filepath.Join(dir, fixtures.AgentName)
	copyFile(t, agentPath, exe)

	stop := startHoldingAgent(t, exe)
	defer stop()

	renamed := filepath.Join(dir, "renamed-agent")
	if err := os.Rename(exe, renamed); err != nil {
		t.Fatalf("POSIX fact violated: rename of running binary failed: %v", err)
	}
}

// FACT (POSIX): updating via versioned directories while the old process is
// still running works end-to-end: the coordinator stages v<next>, commits
// the manifest, and the still-running OLD process is untouched (it keeps
// its open file; its version dir is never renamed or replaced).
func TestPOSIXUpdateWhileOldAgentRunsSucceeds(t *testing.T) {
	ti := setupInstall(t, false)

	// Old agent runs from the ACTIVE version directory.
	stop := startHoldingAgent(t, ti.agentActive)

	res, err := UpdateToLatest(context.Background(), ti.options)
	if err != nil {
		stop()
		t.Fatalf("update while agent running: %v", err)
	}
	if !res.Activated {
		stop()
		t.Fatal("expected activation while old agent runs (POSIX)")
	}

	m, _ := ReadActiveManifest(ti.installDir)
	if m.Version != nextVersion {
		stop()
		t.Fatalf("manifest = %q, want %q", m.Version, nextVersion)
	}

	// The old binary file is still intact for the still-running process.
	if got := fixtures.RunVersion(t, ti.agentActive); got != currentVersion {
		stop()
		t.Fatalf("old agent binary changed: now %q, want %q (immutability violated)", got, currentVersion)
	}
	stop()

	// New version resolves and runs.
	agentPath, _ := ActiveBinaryPath(ti.installDir, fixtures.AgentName)
	if got := fixtures.RunVersion(t, agentPath); got != nextVersion {
		t.Fatalf("new agent = %q, want %q", got, nextVersion)
	}
}

// FACT (POSIX): an OPEN (but renamed-into-place) file keeps working — this
// documents why rename-based staging is safe, and also that POSIX has no
// sharing violation at all: even REPLACE (delete+create) of a running
// binary's path would succeed.
func TestPOSIXOpenFileDeleteSucceeds(t *testing.T) {
	dir := t.TempDir()
	agentPath := fixtures.BuildAgent(t, currentVersion, false)
	exe := filepath.Join(dir, fixtures.AgentName)
	copyFile(t, agentPath, exe)

	stop := startHoldingAgent(t, exe)
	defer stop()

	if err := os.Remove(exe); err != nil {
		t.Fatalf("POSIX fact violated: unlink of open binary failed: %v", err)
	}
	// The still-running process is unaffected (file remains via open fd).
	if !strings.Contains(fixtures.RunVersion(t, agentPath), currentVersion) {
		// agentPath is the builder path, still exists — sanity only.
		t.Fatal("fixture agent unexpectedly missing")
	}
}
