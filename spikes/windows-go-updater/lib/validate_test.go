package lib

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	selfupdate "github.com/creativeprojects/go-selfupdate"

	"github.com/bryancostanich/Cercano/spikes/windows-go-updater/fixtures"
)

// FACT under test: the stock SHAValidator works against the EXACT sidecar
// format Cercano already publishes ("<hex>  <basename>\n" — the first 64
// bytes of the file are the digest). No new release metadata is required.
// The validator is EXPLICIT (Config.Validator), not a default.
func TestSHAValidatorAcceptsExactCercanoSidecarFormat(t *testing.T) {
	zipPath, shaPath, _, _ := buildCercanoRelease(t)
	src := newSource(t, nextReleaseSpec(t, zipPath, shaPath))
	up := newUpdater(t, src, &selfupdate.SHAValidator{})

	target := filepath.Join(t.TempDir(), fixtures.AgentName)
	copyFile(t, fixtures.BuildAgent(t, currentVersion, false), target)

	rel := detectLatest(t, up)
	if err := up.UpdateTo(context.Background(), rel, target); err != nil {
		t.Fatalf("UpdateTo with SHAValidator: %v", err)
	}
	if got := fixtures.RunVersion(t, target); got != nextVersion {
		t.Fatalf("updated target version = %q, want %q", got, nextVersion)
	}
	// Validation fetched both the archive and the sidecar.
	src.mu.Lock()
	zipDL := src.Downloads[fixtures.AssetName(nextVersion)]
	shaDL := src.Downloads[fixtures.AssetName(nextVersion)+".sha256"]
	src.mu.Unlock()
	if zipDL != 1 || shaDL != 1 {
		t.Fatalf("downloads: zip=%d sha256=%d, want 1/1", zipDL, shaDL)
	}
}

// FACT under test: a tampered archive whose digest does not match the
// published sidecar is rejected by SHAValidator BEFORE any bytes are applied
// to the target; the running install is untouched.
func TestSHAValidatorRejectsTamperedArchiveBeforeApply(t *testing.T) {
	dir := t.TempDir()
	agentPath := fixtures.BuildAgent(t, nextVersion, false)
	cliPath := fixtures.BuildCli(t, nextVersion)
	zipPath, shaPath := fixtures.MakeCercanoRelease(t, dir, nextVersion, agentPath, cliPath)

	// Tamper: same archive layout, but the agent binary inside is a
	// DIFFERENT binary (evilVersion). Keep the original sidecar: the digest
	// will not match the tampered archive.
	evilAgent := fixtures.BuildAgent(t, evilVersion, false)
	tamperedPath, _ := fixtures.MakeCercanoRelease(t, dir+"-tampered", nextVersion, evilAgent, cliPath)

	target := filepath.Join(t.TempDir(), fixtures.AgentName)
	copyFile(t, fixtures.BuildAgent(t, currentVersion, false), target)

	src := newSource(t, nextReleaseSpec(t, tamperedPath, shaPath))
	up := newUpdater(t, src, &selfupdate.SHAValidator{})

	rel := detectLatest(t, up)
	err := up.UpdateTo(context.Background(), rel, target)
	if err == nil {
		t.Fatal("expected SHAValidator to reject the tampered archive")
	}
	if !errors.Is(err, selfupdate.ErrChecksumValidationFailed) {
		t.Logf("rejection error (not ErrChecksumValidationFailed, still rejected): %v", err)
	}
	// The running install must be untouched.
	if got := fixtures.RunVersion(t, target); got != currentVersion {
		t.Fatalf("target was modified: version = %q, want %q", got, currentVersion)
	}
	if bytes.Equal(mustRead(t, target), mustRead(t, zipPath)) {
		t.Fatal("sanity: target unexpectedly equals archive bytes")
	}
}

// FACT under test (opt-in, not default): with NO validator configured, the
// stock updater applies whatever bytes the archive contains — an attacker
// who can replace the archive (but not the checksum sidecar they also
// control, since the updater only checks if told to) gets their binary
// installed. Integrity checking must be configured explicitly.
func TestNoValidationByDefaultAppliesTamperedBytes(t *testing.T) {
	dir := t.TempDir()
	agentPath := fixtures.BuildAgent(t, nextVersion, false)
	cliPath := fixtures.BuildCli(t, nextVersion)
	_, shaPath := fixtures.MakeCercanoRelease(t, dir, nextVersion, agentPath, cliPath)

	evilAgent := fixtures.BuildAgent(t, evilVersion, false)
	tamperedPath, _ := fixtures.MakeCercanoRelease(t, dir+"-tampered", nextVersion, evilAgent, cliPath)

	target := filepath.Join(t.TempDir(), fixtures.AgentName)
	copyFile(t, fixtures.BuildAgent(t, currentVersion, false), target)

	src := newSource(t, nextReleaseSpec(t, tamperedPath, shaPath))
	up := newUpdater(t, src, nil) // NO validator: stock default

	rel := detectLatest(t, up)
	if err := up.UpdateTo(context.Background(), rel, target); err != nil {
		t.Fatalf("UpdateTo without validator: %v", err)
	}
	if got := fixtures.RunVersion(t, target); got != evilVersion {
		t.Fatalf("stock default applied version = %q, want %q (documents opt-in-only validation)", got, evilVersion)
	}
}
