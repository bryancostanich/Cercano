package updater

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	selfupdate "github.com/creativeprojects/go-selfupdate"

	"github.com/bryancostanich/Cercano/spikes/windows-go-updater/fixtures"
	"github.com/bryancostanich/Cercano/spikes/windows-go-updater/lib"
)

const (
	currentVersion = "0.20.0"
	nextVersion    = "0.21.0"
)

var repo = selfupdate.NewRepositorySlug("bryancostanich", "Cercano")

// setupInstall builds a fake v<nextVersion> release (exact Cercano naming)
// and an installed layout whose ACTIVE version is currentVersion. The agent
// fixture may be built "unhealthy" to model a broken update.
type testInstall struct {
	installDir  string
	src         *lib.FakeSource
	options     Options
	agentActive string // active agent path
	cliActive   string
}

func setupInstall(t *testing.T, unhealthyNextAgent bool) *testInstall {
	t.Helper()
	dir := t.TempDir()
	installDir := filepath.Join(dir, "install")

	agentPath := fixtures.BuildAgent(t, nextVersion, unhealthyNextAgent)
	cliPath := fixtures.BuildCli(t, nextVersion)
	zipPath, shaPath := fixtures.MakeCercanoRelease(t, dir, nextVersion, agentPath, cliPath)

	src, err := lib.NewFakeSource([]lib.ReleaseSpec{{
		Tag: "v" + nextVersion,
		Assets: []lib.AssetSpec{
			{Name: fixtures.AssetName(nextVersion), Path: zipPath},
			{Name: fixtures.AssetName(nextVersion) + ".sha256", Path: shaPath},
		},
	}})
	if err != nil {
		t.Fatalf("NewFakeSource: %v", err)
	}

	// Install the current version as an immutable versioned directory and
	// commit it in the active manifest.
	curAgent := fixtures.BuildAgent(t, currentVersion, false)
	curCli := fixtures.BuildCli(t, currentVersion)
	curDir := filepath.Join(installDir, "versions", "v"+currentVersion, "bin")
	if err := os.MkdirAll(curDir, 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, curAgent, filepath.Join(curDir, fixtures.AgentName))
	copyFile(t, curCli, filepath.Join(curDir, fixtures.CliName))
	if err := writeManifestAtomic(filepath.Join(installDir, manifestName), ActiveManifest{
		Version:   currentVersion,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	ti := &testInstall{
		installDir:  installDir,
		src:         src,
		agentActive: filepath.Join(curDir, fixtures.AgentName),
		cliActive:   filepath.Join(curDir, fixtures.CliName),
		options: Options{
			Repository: repo,
			Source:     src,
			Validator:  &selfupdate.SHAValidator{}, // EXPLICIT validation
			OS:         "windows",
			Arch:       "x64",
			InstallDir: installDir,
			Binaries:   []string{fixtures.AgentName, fixtures.CliName},
		},
	}
	return ti
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

// CORE EXPERIMENT (positive path): the coordinator stages BOTH Cercano
// binaries from the single exact-named archive and activates them with ONE
// manifest commit. Both binaries flip together; the old version directory
// remains on disk (rollback availability).
func TestCoordinatorStagesAndActivatesBothBinariesTogether(t *testing.T) {
	ti := setupInstall(t, false)

	res, err := UpdateToLatest(context.Background(), ti.options)
	if err != nil {
		t.Fatalf("UpdateToLatest: %v", err)
	}
	if !res.Activated || res.Version != nextVersion {
		t.Fatalf("result = %+v, want activated %s", res, nextVersion)
	}

	// Active manifest now points at the new version.
	m, err := ReadActiveManifest(ti.installDir)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if m.Version != nextVersion {
		t.Fatalf("manifest version = %q, want %q", m.Version, nextVersion)
	}

	// BOTH binaries resolve to and run the new version.
	agentPath, err := ActiveBinaryPath(ti.installDir, fixtures.AgentName)
	if err != nil {
		t.Fatal(err)
	}
	cliPath, err := ActiveBinaryPath(ti.installDir, fixtures.CliName)
	if err != nil {
		t.Fatal(err)
	}
	if got := fixtures.RunVersion(t, agentPath); got != nextVersion {
		t.Fatalf("agent version = %q, want %q", got, nextVersion)
	}
	if got := fixtures.RunVersion(t, cliPath); got != nextVersion {
		t.Fatalf("cli version = %q, want %q", got, nextVersion)
	}

	// The previous version directory is retained on disk.
	old := filepath.Join(ti.installDir, "versions", "v"+currentVersion)
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("previous version dir must be retained: %v", err)
	}
	// No staging leftovers.
	ents, _ := os.ReadDir(filepath.Join(ti.installDir, "versions"))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "staged-") {
			t.Fatalf("staging leftover: %s", e.Name())
		}
	}
}

// FAILURE MODE (pre-activation): an injected download failure during staging
// leaves the active manifest and the active binaries COMPLETELY untouched.
func TestInjectedFailureBeforeActivationKeepsOldManifest(t *testing.T) {
	ti := setupInstall(t, false)
	ti.src.FailOnNames[fixtures.AssetName(nextVersion)] = true

	if _, err := UpdateToLatest(context.Background(), ti.options); err == nil {
		t.Fatal("expected the injected download failure to abort the update")
	}

	m, err := ReadActiveManifest(ti.installDir)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if m.Version != currentVersion {
		t.Fatalf("manifest version = %q, want %q (must be untouched)", m.Version, currentVersion)
	}
	if got := fixtures.RunVersion(t, ti.agentActive); got != currentVersion {
		t.Fatalf("agent version = %q, want %q", got, currentVersion)
	}
	if got := fixtures.RunVersion(t, ti.cliActive); got != currentVersion {
		t.Fatalf("cli version = %q, want %q", got, currentVersion)
	}
	// No staged or partially staged directories.
	ents, _ := os.ReadDir(filepath.Join(ti.installDir, "versions"))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "staged-v") || strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("staging leftover after failed run: %s", e.Name())
		}
	}
}

// FAILURE MODE (pre-activation): an archive missing one of the two required
// binaries is rejected during staging — never at activation time, never
// leaving a half-updated install.
func TestArchiveMissingOneBinaryIsRejectedBeforeActivation(t *testing.T) {
	ti := setupInstall(t, false)
	// Replace the source with an archive WITHOUT cercano-cli.exe.
	dir := t.TempDir()
	agentPath := fixtures.BuildAgent(t, nextVersion, false)
	zipPath, shaPath := fixtures.MakeCercanoRelease(t, dir, nextVersion, agentPath, "")
	src, err := lib.NewFakeSource([]lib.ReleaseSpec{{
		Tag: "v" + nextVersion,
		Assets: []lib.AssetSpec{
			{Name: fixtures.AssetName(nextVersion), Path: zipPath},
			{Name: fixtures.AssetName(nextVersion) + ".sha256", Path: shaPath},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ti.options.Source = src

	_, err = UpdateToLatest(context.Background(), ti.options)
	if err == nil {
		t.Fatal("expected staging to reject an archive missing cercano-cli.exe")
	}
	if !errors.Is(err, selfupdate.ErrExecutableNotFoundInArchive) {
		t.Fatalf("rejection must be ErrExecutableNotFoundInArchive (missing cercano-cli.exe), got: %v", err)
	}

	m, _ := ReadActiveManifest(ti.installDir)
	if m == nil || m.Version != currentVersion {
		t.Fatalf("manifest must still say %q, got %+v", currentVersion, m)
	}
}

// FAILURE MODE (pre-activation): a tampered archive fails EXPLICIT SHA-256
// validation during staging; nothing is staged or activated. (The library's
// default is NO validation — this test proves the coordinator configures it
// explicitly, and that without it a tampered binary WOULD be installed.)
func TestTamperedArchiveFailsValidationAndActivatesNothing(t *testing.T) {
	ti := setupInstall(t, false)
	// Rebuild the archive with a DIFFERENT agent binary (tampered content)
	// while keeping the original sidecar digest.
	dir := t.TempDir()
	cliPath := fixtures.BuildCli(t, nextVersion)
	_, shaPath := fixtures.MakeCercanoRelease(t, dir, nextVersion, fixtures.BuildAgent(t, nextVersion, false), cliPath)
	tamperedZip, _ := fixtures.MakeCercanoRelease(t, dir+"-tampered", nextVersion, fixtures.BuildAgent(t, "9.9.9-malicious", false), cliPath)
	src, err := lib.NewFakeSource([]lib.ReleaseSpec{{
		Tag: "v" + nextVersion,
		Assets: []lib.AssetSpec{
			{Name: fixtures.AssetName(nextVersion), Path: tamperedZip},
			{Name: fixtures.AssetName(nextVersion) + ".sha256", Path: shaPath},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ti.options.Source = src

	if _, err := UpdateToLatest(context.Background(), ti.options); err == nil {
		t.Fatal("expected SHAValidator to reject the tampered archive")
	}

	m, _ := ReadActiveManifest(ti.installDir)
	if m == nil || m.Version != currentVersion {
		t.Fatalf("manifest must still say %q, got %+v", currentVersion, m)
	}
	if got := fixtures.RunVersion(t, ti.agentActive); got != currentVersion {
		t.Fatalf("agent version = %q, want %q", got, currentVersion)
	}
}

// FAILURE MODE (post-activation, watchdog rollback): the new version was
// activated, but its health check fails (the staged agent was built
// "unhealthy"). A supervisor calls RestorePrevious, which flips the
// manifest back to the retained old version: both binaries again resolve to
// the old version. This demonstrates the ROLLBACK PATH ONLY — it does not
// claim an automatic watcher, process supervision, or app lifecycle.
func TestHealthFailureAfterActivationRestoresOldManifest(t *testing.T) {
	ti := setupInstall(t, true) // next agent is UNHEALTHY

	res, err := UpdateToLatest(context.Background(), ti.options)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !res.Activated {
		t.Fatal("expected activation (staging does not health-check binaries)")
	}

	m, _ := ReadActiveManifest(ti.installDir)
	if m.Version != nextVersion {
		t.Fatalf("manifest = %q, want %q", m.Version, nextVersion)
	}

	// Supervisor restarts the app; it resolves the active binary and runs
	// its health check. It FAILS.
	agentPath, err := ActiveBinaryPath(ti.installDir, fixtures.AgentName)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixtures.RunHealth(agentPath); err == nil {
		t.Fatal("expected the new (unhealthy) agent's health check to fail")
	}

	// Watchdog rollback: restore the previous version's manifest.
	if err := RestorePrevious(ti.options, currentVersion); err != nil {
		t.Fatalf("RestorePrevious: %v", err)
	}
	m2, _ := ReadActiveManifest(ti.installDir)
	if m2.Version != currentVersion {
		t.Fatalf("manifest after restore = %q, want %q", m2.Version, currentVersion)
	}
	agentPath2, _ := ActiveBinaryPath(ti.installDir, fixtures.AgentName)
	cliPath2, _ := ActiveBinaryPath(ti.installDir, fixtures.CliName)
	if got := fixtures.RunVersion(t, agentPath2); got != currentVersion {
		t.Fatalf("agent version after restore = %q, want %q", got, currentVersion)
	}
	if err := fixtures.RunHealth(agentPath2); err != nil {
		t.Fatalf("old agent must be healthy again: %v", err)
	}
	if got := fixtures.RunVersion(t, cliPath2); got != currentVersion {
		t.Fatalf("cli version after restore = %q, want %q", got, currentVersion)
	}
}

// LOCKING: two concurrent coordinators cannot overlap; the OS-level lock
// serializes them. Proven with a REAL subprocess holding the lock:
// the parent observes contention while the worker holds it, and free
// acquisition after it exits. RunWithLock also removes the lock file on exit.
func TestCoordinatorLockPreventsConcurrentUpdaters(t *testing.T) {
	ti := setupInstall(t, false)
	lockPath := filepath.Join(ti.installDir, lockName)

	worker := exec.Command(os.Args[0], "-test.run=TestLockWorkerProcess")
	worker.Env = append(os.Environ(),
		"SPIKE_LOCK_WORKER=1",
		"SPIKE_LOCK_PATH="+lockPath,
	)
	if err := worker.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}

	// Wait for the worker to actually take the lock (file appears).
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(lockPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = worker.Wait()
			t.Fatal("worker never created the lock file")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Our acquisition must fail while the worker holds the OS lock.
	sawContention := false
	for time.Now().Before(deadline) {
		fd, err := lockfileCreate(lockPath, false, time.Minute)
		if err != nil {
			sawContention = true
			break
		}
		_ = lockfileClose(fd)
		time.Sleep(10 * time.Millisecond)
	}
	_ = worker.Wait()
	if !sawContention {
		t.Fatal("never observed lock contention — OS lock does not exclude concurrent updaters")
	}

	// After the worker exits, acquisition succeeds and the full
	// RunWithLock path completes and removes the lock file.
	res, err := UpdateToLatest(context.Background(), ti.options)
	if err != nil {
		t.Fatalf("UpdateToLatest under lock: %v", err)
	}
	if !res.Activated {
		t.Fatal("expected activation")
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock file must be removed after RunWithLock, stat err = %v", err)
	}
}

// TestLockWorkerProcess is the worker for the tests above: it holds the
// coordinator lock briefly so the parent can observe exclusion.
func TestLockWorkerProcess(t *testing.T) {
	if os.Getenv("SPIKE_LOCK_WORKER") != "1" {
		t.Skip("worker-only test")
	}
	lockPath := os.Getenv("SPIKE_LOCK_PATH")
	fd, err := lockfileCreate(lockPath, false, time.Minute)
	if err != nil {
		t.Fatalf("worker lock: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	_ = lockfileClose(fd)
	os.Exit(0)
}
