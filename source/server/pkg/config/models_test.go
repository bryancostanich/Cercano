package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveOpen pins the open-tier-resolution contract: a configured tier
// yields its open model, and an empty tier is !ok. Cloud is NOT resolved here
// — it flows through the vendor-keyed cost-tier path — so ResolveOpen reads
// only the open slot and never crosses providers.
func TestResolveOpen(t *testing.T) {
	m := ModelsConfig{
		Tiers: ModelTiers{
			Everyday:      ModelTier{Open: "qwen3-coder"},
			FastLightText: ModelTier{Open: "phi4:14b"},
		},
	}

	cases := []struct {
		name   string
		tier   Tier
		wantID string
		wantOK bool
	}{
		{"everyday open", TierEveryday, "qwen3-coder", true},
		{"text tier open", TierFastLightText, "phi4:14b", true},
		{"unconfigured tier", TierMostCapable, "", false},
		{"empty open slot", TierFastLight, "", false},
	}
	for _, c := range cases {
		id, ok := m.ResolveOpen(c.tier)
		if id != c.wantID || ok != c.wantOK {
			t.Errorf("%s: ResolveOpen(%s) = (%q,%v), want (%q,%v)",
				c.name, c.tier, id, ok, c.wantID, c.wantOK)
		}
	}
}

// TestModelsYAMLRoundTrip pins the on-disk shape: a models section with a
// tiers map including the fast_light_text tier's open slot.
func TestModelsYAMLRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	src := `ollama_url: http://localhost:11434
models:
    tiers:
        fast_light_text:
            open: phi4:14b
`
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
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

// TestApplyModelTierPatch pins the sparse-patch contract used by /config:
// "<tier>.open" sets a slot, "-" clears it, and unknown tiers/providers are
// rejected with an error. (Cloud is not patchable here — vendor-keyed path.)
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

	// Cloud is no longer a patchable provider side.
	if _, err := ApplyModelTierPatch(&m, "everyday.cloud", "claude-opus"); err == nil {
		t.Error("everyday.cloud must be rejected — cloud is not a tier slot")
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
	m.Tiers.Everyday.Open = "qwen3-coder"

	slots := m.TierSlots()
	if len(slots) != 2 {
		t.Fatalf("slots = %v, want 2 entries", slots)
	}
	if slots["fast_light_text.open"] != "phi4:14b" || slots["everyday.open"] != "qwen3-coder" {
		t.Errorf("slots = %v", slots)
	}
}
