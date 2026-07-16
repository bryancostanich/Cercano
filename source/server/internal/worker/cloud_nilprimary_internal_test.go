package worker

// cloud_nilprimary_internal_test.go — regression guard for the production
// nil-pointer panic in dispatch.Select.
//
// When the ACTIVE profile's credential fetch failed, buildWorkerProviders left
// the primary provider nil but still wrapped it in a fallback composite (a
// configured backup). That produced a non-nil inference.Provider interface holding a
// fallback whose .primary is nil — so dispatch.Select's `p.Cloud != nil` is true
// but `p.Cloud.Name()` (fallback.Name -> p.primary.Name) nil-derefs. Repro of
// the real crash: active/meridian profile with no keychain key + a chatgpt
// backend backup.

import (
	"context"
	"errors"
	"testing"

	pkgcfg "cercano/source/server/pkg/config"
)

// splitFetcher fails the primary credential fetch but succeeds for the backup —
// the exact condition that built a nil-primary fallback.
type splitFetcher struct{ backupName string }

func (f *splitFetcher) Fetch(_ context.Context, name string) (string, string, error) {
	if name == f.backupName {
		return "bkp-key", "", nil
	}
	return "", "", errors.New("no credential for " + name)
}

func TestBuildWorkerProviders_PrimaryFetchFail_NoNilFallbackPanic(t *testing.T) {
	cfg := pkgcfg.Config{
		LocusMode:          "cloud_primary",
		OllamaURL:          "http://localhost:11434", // open exists for graceful degradation
		ActiveCloudProfile: "primary",
		BackupCloudProfile: "bkp",
		CloudProfiles: []pkgcfg.CloudProfile{
			// Primary: no BaseURL, no key -> unauthable -> cloud must be left unset.
			{Name: "primary", Flavor: "messages", Route: "direct"},
			// Backup: static key + BaseURL -> builds without network.
			{Name: "bkp", Flavor: "messages", Route: "direct", BaseURL: "http://127.0.0.1:9999"},
		},
		Models: pkgcfg.ModelsConfig{Tiers: pkgcfg.ModelTiers{Everyday: pkgcfg.ModelTier{Open: "qwen"}}},
	}

	r, err := buildWorkerProviders(context.Background(), cfg, &splitFetcher{backupName: "bkp"}, nil)
	if err != nil {
		t.Fatalf("buildWorkerProviders: %v", err)
	}

	// Pre-fix: r.cloudProv is a fallback wrapping a NIL primary; Main() ->
	// dispatch.Select -> p.Cloud.Name() panics. Post-fix: cloud is unset and Main
	// gracefully degrades to the open provider.
	prov, isCloud, _, err := r.Main()
	if err != nil {
		t.Fatalf("Main returned error: %v", err)
	}
	if prov == nil {
		t.Fatal("Main returned nil provider")
	}
	_ = prov.Name() // must not panic
	if isCloud {
		t.Errorf("expected degrade to open (isCloud=false) when the primary is unauthable, got a cloud provider")
	}
}
