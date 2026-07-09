package worker

// providers_internal_test.go — regression guard for active-profile selection.
//
// buildWorkerProviders must build the cloud provider from the ACTIVE profile,
// selected by NAME (mirroring the host's rebuildCloud / ActiveProfile). A prior
// bug selected CloudProfiles[0], so whenever the active profile wasn't first the
// worker built the wrong provider (route/flavor/credential-profile) while its
// own model resolution used the correct named profile — a silent divergence.

import (
	"context"
	"errors"
	"testing"

	pkgcfg "cercano/source/server/pkg/config"
)

// recordingFetcher records the profile names it is asked to resolve. Returning
// an error short-circuits the cloud-provider build (the code logs and continues
// without cloud), so the test asserts SELECTION without depending on
// cloudfactory building a real provider from a fake credential.
type recordingFetcher struct{ names []string }

func (f *recordingFetcher) Fetch(_ context.Context, name string) (string, string, error) {
	f.names = append(f.names, name)
	return "", "", errors.New("test: no credential")
}

func TestBuildWorkerProviders_SelectsActiveProfileByName(t *testing.T) {
	cfg := pkgcfg.Config{
		CloudProfiles: []pkgcfg.CloudProfile{
			{Name: "alpha"}, // index 0 — must NOT be chosen
			{Name: "bravo"}, // the active profile
		},
		ActiveCloudProfile: "bravo",
	}

	f := &recordingFetcher{}
	if _, err := buildWorkerProviders(context.Background(), cfg, f); err != nil {
		t.Fatalf("buildWorkerProviders: %v", err)
	}

	if len(f.names) == 0 {
		t.Fatal("no credential fetch — the active profile was never selected")
	}
	if f.names[0] != "bravo" {
		t.Fatalf("built cloud provider from profile %q, want active %q "+
			"(the index-0 bug picks %q)", f.names[0], "bravo", "alpha")
	}
}
