package lib

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	selfupdate "github.com/creativeprojects/go-selfupdate"

	"github.com/bryancostanich/Cercano/spikes/windows-go-updater/fixtures"
)

// setupInstall creates an "installed" layout of two Cercano binaries at
// currentVersion and returns their paths plus a validating updater for the
// next release. This mirrors UpdateCommand usage: the caller updates ONE
// command path at a time.
func setupInstall(t *testing.T) (agentPath, cliPath string, src *FakeSource, up *selfupdate.Updater) {
	t.Helper()
	zipPath, shaPath, _, _ := buildCercanoRelease(t)
	src = newSource(t, nextReleaseSpec(t, zipPath, shaPath))
	up = newUpdater(t, src, &selfupdate.SHAValidator{})

	install := t.TempDir()
	agentPath = filepath.Join(install, fixtures.AgentName)
	cliPath = filepath.Join(install, fixtures.CliName)
	copyFile(t, fixtures.BuildAgent(t, currentVersion, false), agentPath)
	copyFile(t, fixtures.BuildCli(t, currentVersion), cliPath)
	return agentPath, cliPath, src, up
}

// FACT under test (stock behavior for two binaries, part 1): UpdateCommand
// replaces ONLY the executable at the given command path. After updating
// cercano.exe, cercano-cli.exe still runs the OLD version. There is no
// stock mechanism that updates both binaries together.
func TestStockUpdateCommandReplacesOnlyTheTargetedBinary(t *testing.T) {
	agentPath, cliPath, src, up := setupInstall(t)

	if _, err := up.UpdateCommand(context.Background(), agentPath, currentVersion, repo); err != nil {
		t.Fatalf("UpdateCommand(agent): %v", err)
	}

	if got := fixtures.RunVersion(t, agentPath); got != nextVersion {
		t.Fatalf("agent version = %q, want %q", got, nextVersion)
	}
	if got := fixtures.RunVersion(t, cliPath); got != currentVersion {
		t.Fatalf("cli version = %q, want %q (UpdateCommand must not touch other binaries)", got, currentVersion)
	}

	// The archive was downloaded exactly once, for the single targeted binary.
	src.mu.Lock()
	zipDL := src.Downloads[fixtures.AssetName(nextVersion)]
	src.mu.Unlock()
	if zipDL != 1 {
		t.Fatalf("archive downloads = %d, want 1", zipDL)
	}

	// On POSIX the replaced binary's save file is cleaned up after install.
	// (update.Apply keeps the old binary as ".old" until program exit only
	// when OldSavePath is set; here it must not linger in the bin dir.)
	for _, leftover := range []string{agentPath + ".old", agentPath + ".new"} {
		if _, err := os.Stat(leftover); err == nil {
			t.Errorf("leftover file %s still present after update", leftover)
		}
	}
}

// FACT under test (stock behavior for two binaries, part 2): updating both
// Cercano binaries is TWO independent operations. If the second operation
// fails, the first one is already applied and NOT rolled back — the install
// is left in a MIXED state (agent new, cli old). The stock library has no
// multi-binary transaction.
func TestStockTwoBinaryUpdateIsTwoIndependentNonAtomicOperations(t *testing.T) {
	agentPath, cliPath, src, up := setupInstall(t)

	// First operation succeeds.
	if _, err := up.UpdateCommand(context.Background(), agentPath, currentVersion, repo); err != nil {
		t.Fatalf("UpdateCommand(agent): %v", err)
	}

	// Inject a download failure for the second operation (e.g. network died
	// between the two updates).
	src.mu.Lock()
	src.FailOnNames[fixtures.AssetName(nextVersion)] = true
	src.mu.Unlock()

	if _, err := up.UpdateCommand(context.Background(), cliPath, currentVersion, repo); err == nil {
		t.Fatal("expected the second UpdateCommand to fail with the injected download failure")
	} else {
		t.Logf("second operation failed as injected: %v", err)
	}

	agent := fixtures.RunVersion(t, agentPath)
	cli := fixtures.RunVersion(t, cliPath)
	if agent != nextVersion || cli != currentVersion {
		t.Fatalf("mixed state after failure: agent=%q cli=%q; the stock updater applies NO rollback across binaries", agent, cli)
	}

	// The archive is re-downloaded per operation: no cross-binary reuse.
	src.mu.Lock()
	zipDL := src.Downloads[fixtures.AssetName(nextVersion)]
	src.mu.Unlock()
	if zipDL != 2 {
		t.Fatalf("archive downloads = %d, want 2 (one per binary update)", zipDL)
	}
}
