package worker

import (
	"slices"
	"testing"

	"cercano/source/server/pkg/config"
)

// TestSnapshotConfigCarriesNonActiveDeepinfraProfile verifies that a DeepInfra
// cloud profile that is NOT the active (or backup) profile still survives the
// SnapshotConfig → ConfigFromSnapshot round trip — as a sanitized stub entry
// (name + exact native base URL, no credentials) — so deepinfra_infer can
// select it via the 'profile' argument in the crash-isolated worker.
func TestSnapshotConfigCarriesNonActiveDeepinfraProfile(t *testing.T) {
	orig := config.Config{
		LocusMode:          "cloud_primary",
		ActiveCloudProfile: "myprofile",
		CloudProfiles: []config.CloudProfile{
			{Name: "myprofile", Flavor: "messages", BaseURL: "https://api.anthropic.com", Model: "claude-opus-4-5"},
			// DeepInfra profile that is not active/backup: used only by
			// deepinfra_infer, must still be carried (name + host only).
			{Name: "di-side", Flavor: "chat_completions", Backend: "openai", Provider: "deepinfra", BaseURL: "https://api.deepinfra.com/v1/openai"},
			// Foreign-host profile labelled deepinfra must NOT be carried.
			{Name: "di-fake", Provider: "deepinfra", BaseURL: "https://evil.example/v1"},
		},
	}
	snap := SnapshotConfig(orig, "cred", nil)
	if got := snap.DeepinfraProfiles; !slices.Equal(got, []string{"di-side"}) {
		t.Fatalf("snapshot DeepinfraProfiles = %v, want exactly [di-side] (name only, no credentials)", got)
	}

	got := ConfigFromSnapshot(snap)
	idx := slices.IndexFunc(got.CloudProfiles, func(p config.CloudProfile) bool { return p.Name == "di-side" })
	if idx < 0 {
		t.Fatalf("ConfigFromSnapshot dropped the non-active DeepInfra profile; profiles = %+v", got.CloudProfiles)
	}
	p := got.CloudProfiles[idx]
	if p.Provider != "deepinfra" {
		t.Errorf("rebuilt profile Provider = %q, want deepinfra", p.Provider)
	}
	if p.BaseURL != config.DeepinfraNativeBaseURL {
		t.Errorf("rebuilt profile BaseURL = %q, want exact native base %q", p.BaseURL, config.DeepinfraNativeBaseURL)
	}
	if p.Model != "" {
		t.Errorf("rebuilt stub profile carries Model %q, want empty stub", p.Model)
	}
	// Eligibility must hold after the round trip (exact host match).
	if config.DeepinfraHost(p) != "api.deepinfra.com" {
		t.Errorf("rebuilt profile not eligible for deepinfra_infer credentials (host %q)", config.DeepinfraHost(p))
	}
	if slices.ContainsFunc(got.CloudProfiles, func(p config.CloudProfile) bool { return p.Name == "di-fake" }) {
		t.Errorf("foreign-host profile %q was rebuilt from the snapshot; it must never be eligible", "di-fake")
	}
}
