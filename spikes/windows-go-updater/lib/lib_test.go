package lib

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	selfupdate "github.com/creativeprojects/go-selfupdate"

	"github.com/bryancostanich/Cercano/spikes/windows-go-updater/fixtures"
)

// Versions used across the experiments. Cercano releases use stable X.Y.Z
// versions and v-prefixed git tags; the Windows asset name embeds the
// version and the literal "windows-x64" (NOT "windows-amd64").
const (
	currentVersion = "0.20.0"
	nextVersion    = "0.21.0"
	evilVersion    = "9.9.9-malicious"
)

var repo = selfupdate.NewRepositorySlug("bryancostanich", "Cercano")

// buildCercanoRelease builds HOST fixture binaries and packages them with the
// exact current Cercano Windows naming and layout for nextVersion.
func buildCercanoRelease(t *testing.T) (zipPath, shaPath, agentPath, cliPath string) {
	t.Helper()
	dir := t.TempDir()
	agentPath = fixtures.BuildAgent(t, nextVersion, false)
	cliPath = fixtures.BuildCli(t, nextVersion)
	zipPath, shaPath = fixtures.MakeCercanoRelease(t, dir, nextVersion, agentPath, cliPath)
	return zipPath, shaPath, agentPath, cliPath
}

// newSource constructs a FakeSource or fails the test.
func newSource(t *testing.T, specs ...ReleaseSpec) *FakeSource {
	t.Helper()
	s, err := NewFakeSource(specs)
	if err != nil {
		t.Fatalf("NewFakeSource: %v", err)
	}
	return s
}

// newUpdater builds a real selfupdate.Updater against the fake source with
// Cercano's windows/x64 identity (validator optional).
func newUpdater(t *testing.T, src *FakeSource, validator selfupdate.Validator) *selfupdate.Updater {
	t.Helper()
	up, err := selfupdate.NewUpdater(selfupdate.Config{
		Source:    src,
		OS:        "windows",
		Arch:      "x64",
		Validator: validator,
	})
	if err != nil {
		t.Fatalf("NewUpdater: %v", err)
	}
	return up
}

// nextReleaseSpec describes the published v<nextVersion> fixture release with
// both Cercano assets (archive + .sha256 sidecar).
func nextReleaseSpec(t *testing.T, zipPath, shaPath string) ReleaseSpec {
	t.Helper()
	return ReleaseSpec{
		Tag: "v" + nextVersion,
		Assets: []AssetSpec{
			{Name: fixtures.AssetName(nextVersion), Path: zipPath},
			{Name: fixtures.AssetName(nextVersion) + ".sha256", Path: shaPath},
		},
	}
}

// copyFile copies src to dst preserving the exec bit.
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

// detectLatest is a convenience wrapper.
func detectLatest(t *testing.T, up *selfupdate.Updater) *selfupdate.Release {
	t.Helper()
	rel, found, err := up.DetectLatest(context.Background(), repo)
	if err != nil {
		t.Fatalf("DetectLatest: %v", err)
	}
	if !found {
		t.Fatal("DetectLatest: release not found")
	}
	return rel
}

// Variant updater constructors used by the detection experiments.

func newUpdaterArch(src *FakeSource, validator selfupdate.Validator, arch string) (*selfupdate.Updater, error) {
	return selfupdate.NewUpdater(selfupdate.Config{
		Source: src, OS: "windows", Arch: arch, Validator: validator,
	})
}

func newUpdaterFiltered(src *FakeSource, validator selfupdate.Validator, filter string) (*selfupdate.Updater, error) {
	return selfupdate.NewUpdater(selfupdate.Config{
		Source: src, OS: "windows", Arch: "amd64", Validator: validator,
		Filters: []string{filter},
	})
}

func newUpdaterWithDrafts(src *FakeSource, draft, prerelease bool) (*selfupdate.Updater, error) {
	return selfupdate.NewUpdater(selfupdate.Config{
		Source: src, OS: "windows", Arch: "x64", Draft: draft, Prerelease: prerelease,
	})
}
