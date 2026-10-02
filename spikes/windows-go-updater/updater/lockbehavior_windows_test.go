//go:build windows

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

// WINDOWS-NATIVE TESTS — these are the facts the spike could NOT observe
// locally (host was macOS). They are expected to run on the parent task's
// credential-free Windows CI probe. They intentionally assert the Windows
// semantics the architecture depends on:
//
//   - a running .exe CANNOT be renamed (sharing violation) — hence
//     running_windows.go probes before activation;
//   - Stage() refuses to activate while an active binary is running.

// startHoldingAgent starts a fixture agent with --hold-open (it keeps
// running, holding its own executable open via a real fd, until killed) and
// returns a stop function that kills and reaps it.
func startHoldingAgent(t *testing.T, path string) (stop func()) {
	t.Helper()
	cmd := exec.Command(path, "--hold-open")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start agent: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	return func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}

// FACT (Windows, to be verified on CI): a RUNNING .exe cannot be renamed.
func TestWindowsRunningBinaryRenameFails(t *testing.T) {
	dir := t.TempDir()
	agentPath := fixtures.BuildAgent(t, currentVersion, false)
	exe := filepath.Join(dir, fixtures.AgentName)
	copyFile(t, agentPath, exe)

	stop := startHoldingAgent(t, exe)
	defer stop()

	err := os.Rename(exe, filepath.Join(dir, "renamed-agent"))
	if err == nil {
		t.Fatal("Windows fact violated: rename of a RUNNING exe succeeded — running_windows.go probe assumption is wrong")
	}
}

// BEHAVIOR (Windows, to be verified on CI): the coordinator refuses to
// activate while an ACTIVE binary is running, instead of failing the
// manifest commit halfway.
func TestWindowsActivationBlockedWhileAgentRuns(t *testing.T) {
	ti := setupInstall(t, false)

	stop := startHoldingAgent(t, ti.agentActive)
	defer stop()

	_, err := UpdateToLatest(context.Background(), ti.options)
	if err == nil {
		t.Fatal("expected Stage to refuse activation while active binary runs on Windows")
	}
	if !strings.Contains(err.Error(), fixtures.AgentName) && !strings.Contains(err.Error(), "running") {
		t.Logf("block reason unclear: %v", err)
	}

	// Manifest untouched.
	m, _ := ReadActiveManifest(ti.installDir)
	if m == nil || m.Version != currentVersion {
		t.Fatalf("manifest must remain %q, got %+v", currentVersion, m)
	}
}
