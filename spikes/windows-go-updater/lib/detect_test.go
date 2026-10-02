package lib

import (
	"context"
	"testing"

	"github.com/bryancostanich/Cercano/spikes/windows-go-updater/fixtures"
)

// FACT under test: with Config{OS: "windows", Arch: "x64"}, stock
// DetectLatest finds the exact current Cercano asset
// "cercano-<version>-windows-x64.zip" from tag "v<version>".
func TestDetectLatestMatchesExactCercanoNamingWithArchX64(t *testing.T) {
	zipPath, shaPath, _, _ := buildCercanoRelease(t)
	src := newSource(t, nextReleaseSpec(t, zipPath, shaPath))
	up := newUpdater(t, src, nil)

	rel := detectLatest(t, up)
	if rel.Version() != nextVersion {
		t.Fatalf("detected version = %q, want %q", rel.Version(), nextVersion)
	}
	if rel.AssetName != fixtures.AssetName(nextVersion) {
		t.Fatalf("asset name = %q, want %q", rel.AssetName, fixtures.AssetName(nextVersion))
	}
}

// FACT under test: the stock arch suffixes generated for "amd64"
// (windows_amd64 / windows-amd64, with optional .exe) do NOT match
// Cercano's "windows-x64" asset name. Both the runtime default
// (runtime.GOARCH of the host) and an explicit "amd64" fail to detect.
func TestDetectLatestAmd64SuffixesMissCercanoX64Asset(t *testing.T) {
	zipPath, shaPath, _, _ := buildCercanoRelease(t)
	src := newSource(t, nextReleaseSpec(t, zipPath, shaPath))

	for _, arch := range []string{"amd64", "arm64"} {
		up, err := newUpdaterArch(src, nil, arch)
		if err != nil {
			t.Fatalf("arch %q: %v", arch, err)
		}
		rel, found, err := up.DetectLatest(context.Background(), repo)
		if err != nil {
			t.Fatalf("arch %q: DetectLatest: %v", arch, err)
		}
		if found {
			t.Fatalf("arch %q: unexpectedly found asset %q; stock %s suffixes cannot match windows-x64 naming",
				arch, rel.AssetName, arch)
		}
	}
}

// FACT under test: Config.Filters (regex matched against the asset name)
// is the stock mechanism to select the windows-x64 asset without relying
// on arch suffix matching.
func TestFiltersCanSelectCercanoX64Asset(t *testing.T) {
	zipPath, shaPath, _, _ := buildCercanoRelease(t)
	src := newSource(t, nextReleaseSpec(t, zipPath, shaPath))

	up, err := newUpdaterFiltered(src, nil, `windows-x64\.zip$`)
	if err != nil {
		t.Fatalf("NewUpdater: %v", err)
	}
	rel := detectLatest(t, up)
	if rel.Version() != nextVersion {
		t.Fatalf("detected version = %q, want %q", rel.Version(), nextVersion)
	}
}

// FACT under test: drafts and pre-releases are skipped by default and only
// considered with Config.Draft / Config.Prerelease.
func TestDetectLatestSkipsDraftAndPrereleaseByDefault(t *testing.T) {
	zipPath, shaPath, _, _ := buildCercanoRelease(t)
	spec := func(version string, draft, prerelease bool) ReleaseSpec {
		return ReleaseSpec{
			Tag:        "v" + version,
			Draft:      draft,
			Prerelease: prerelease,
			Assets: []AssetSpec{
				{Name: fixtures.AssetName(version), Path: zipPath},
				{Name: fixtures.AssetName(version) + ".sha256", Path: shaPath},
			},
		}
	}
	src := newSource(t,
		spec(nextVersion, false, false),
		spec("0.22.0", true, false),
		spec("0.23.0", false, true),
	)
	up := newUpdater(t, src, nil)

	if rel := detectLatest(t, up); rel.Version() != nextVersion {
		t.Fatalf("default detect = %q, want %q (draft/prerelease must be skipped)", rel.Version(), nextVersion)
	}

	upDraft, err := newUpdaterWithDrafts(src, true, false)
	if err != nil {
		t.Fatalf("NewUpdater drafts: %v", err)
	}
	if rel := detectLatest(t, upDraft); rel.Version() != "0.22.0" {
		t.Fatalf("draft detect = %q, want 0.22.0", rel.Version())
	}

	upPre, err := newUpdaterWithDrafts(src, false, true)
	if err != nil {
		t.Fatalf("NewUpdater prereleases: %v", err)
	}
	if rel := detectLatest(t, upPre); rel.Version() != "0.23.0" {
		t.Fatalf("prerelease detect = %q, want 0.23.0", rel.Version())
	}
}
