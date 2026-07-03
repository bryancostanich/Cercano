package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestModelsResolve pins the tier-resolution contract: explicit slot wins,
// empty prefer falls back to DefaultProvider, non-strict falls back to the
// other provider side, strict does not, and a fully-empty tier is !ok.
func TestModelsResolve(t *testing.T) {
	m := ModelsConfig{
		DefaultProvider: "open",
		Tiers: ModelTiers{
			Everyday:      ModelTier{Cloud: "claude-sonnet-4-6", Open: "qwen3-coder"},
			FastLight:     ModelTier{Cloud: "claude-haiku-4-5-20251001"},
			FastLightText: ModelTier{Open: "phi4:14b"},
		},
	}

	cases := []struct {
		name     string
		tier     Tier
		prefer   Provider
		strict   bool
		wantID   string
		wantProv Provider
		wantOK   bool
	}{
		{"explicit hit", TierEveryday, ProviderCloud, false, "claude-sonnet-4-6", ProviderCloud, true},
		{"prefer empty uses default provider", TierEveryday, "", false, "qwen3-coder", ProviderOpen, true},
		{"fallback to other side", TierFastLight, ProviderOpen, false, "claude-haiku-4-5-20251001", ProviderCloud, true},
		{"strict blocks fallback", TierFastLight, ProviderOpen, true, "", "", false},
		{"text tier open", TierFastLightText, ProviderOpen, true, "phi4:14b", ProviderOpen, true},
		{"unconfigured tier", TierMostCapable, ProviderCloud, false, "", "", false},
	}
	for _, c := range cases {
		id, prov, ok := m.Resolve(c.tier, c.prefer, c.strict)
		if id != c.wantID || prov != c.wantProv || ok != c.wantOK {
			t.Errorf("%s: Resolve(%s,%s,strict=%v) = (%q,%q,%v), want (%q,%q,%v)",
				c.name, c.tier, c.prefer, c.strict, id, prov, ok, c.wantID, c.wantProv, c.wantOK)
		}
	}
}

// TestModelsYAMLRoundTrip pins the on-disk shape: a models section with
// default_provider and a tiers map including the fast_light_text tier.
func TestModelsYAMLRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	src := `ollama_url: http://localhost:11434
models:
    default_provider: open
    tiers:
        fast_light_text:
            cloud: claude-haiku-4-5-20251001
            open: phi4:14b
`
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Models.DefaultProvider != "open" {
		t.Errorf("DefaultProvider = %q, want open", cfg.Models.DefaultProvider)
	}
	if cfg.Models.Tiers.FastLightText.Open != "phi4:14b" {
		t.Errorf("FastLightText.Open = %q, want phi4:14b", cfg.Models.Tiers.FastLightText.Open)
	}
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, _ := os.ReadFile(path)
	for _, want := range []string{"models:", "tiers:", "fast_light_text:", "phi4:14b"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("saved config missing %q:\n%s", want, out)
		}
	}
}

// TestDefaults_ModelsProvider pins the local-first default provider.
func TestDefaults_ModelsProvider(t *testing.T) {
	if got := Defaults().Models.DefaultProvider; got != "open" {
		t.Errorf("Defaults().Models.DefaultProvider = %q, want open", got)
	}
}

// TestApplyModelTierPatch pins the sparse-patch contract used by /config:
// "<tier>.<provider>" sets a slot, "default_provider" sets the preference,
// "-" clears, and unknown tiers/providers are rejected with an error.
func TestApplyModelTierPatch(t *testing.T) {
	var m ModelsConfig

	desc, err := ApplyModelTierPatch(&m, "fast_light_text.open", "phi4:14b")
	if err != nil {
		t.Fatalf("set slot: %v", err)
	}
	if m.Tiers.FastLightText.Open != "phi4:14b" {
		t.Errorf("slot not set: %+v", m.Tiers.FastLightText)
	}
	if !strings.Contains(desc, "fast_light_text.open") {
		t.Errorf("change description should name the slot, got %q", desc)
	}

	if _, err := ApplyModelTierPatch(&m, "default_provider", "cloud"); err != nil {
		t.Fatalf("set default_provider: %v", err)
	}
	if m.DefaultProvider != ProviderCloud {
		t.Errorf("DefaultProvider = %q", m.DefaultProvider)
	}
	if _, err := ApplyModelTierPatch(&m, "default_provider", "banana"); err == nil {
		t.Error("invalid default_provider value must be rejected")
	}

	if _, err := ApplyModelTierPatch(&m, "fast_light_text.open", "-"); err != nil {
		t.Fatalf("clear slot: %v", err)
	}
	if m.Tiers.FastLightText.Open != "" {
		t.Errorf("slot not cleared: %q", m.Tiers.FastLightText.Open)
	}

	if _, err := ApplyModelTierPatch(&m, "medium_rare.open", "x"); err == nil {
		t.Error("unknown tier must be rejected")
	}
	if _, err := ApplyModelTierPatch(&m, "everyday.hybrid", "x"); err == nil {
		t.Error("unknown provider must be rejected")
	}
	if _, err := ApplyModelTierPatch(&m, "everyday", "x"); err == nil {
		t.Error("missing provider segment must be rejected")
	}
}

// TestModelTierSlots pins the read-side enumeration used by GetConfig and
// /config show: only non-empty slots, keyed "<tier>.<provider>".
func TestModelTierSlots(t *testing.T) {
	var m ModelsConfig
	m.Tiers.FastLightText.Open = "phi4:14b"
	m.Tiers.Everyday.Cloud = "claude-fable-5"

	slots := m.TierSlots()
	if len(slots) != 2 {
		t.Fatalf("slots = %v, want 2 entries", slots)
	}
	if slots["fast_light_text.open"] != "phi4:14b" || slots["everyday.cloud"] != "claude-fable-5" {
		t.Errorf("slots = %v", slots)
	}
}
