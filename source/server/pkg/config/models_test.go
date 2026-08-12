package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOverrideFor(t *testing.T) {
	var m ModelsConfig
	m.SetOverride("llama_server", TierEveryday, "qwen3-coder")
	m.SetOverride("llama_server", TierFastLightText, "phi4:14b")

	cases := []struct {
		name    string
		runtime string
		tier    Tier
		wantID  string
		wantOK  bool
	}{
		{"runtime override", "llama_server", TierEveryday, "qwen3-coder", true},
		{"second override", "llama_server", TierFastLightText, "phi4:14b", true},
		{"other runtime untouched", "mistralrs", TierEveryday, "", false},
		{"unconfigured tier", "llama_server", TierMostCapable, "", false},
	}
	for _, c := range cases {
		id, ok := m.OverrideFor(c.runtime, c.tier)
		if id != c.wantID || ok != c.wantOK {
			t.Errorf("%s: OverrideFor(%s,%s) = (%q,%v), want (%q,%v)",
				c.name, c.runtime, c.tier, id, ok, c.wantID, c.wantOK)
		}
	}
}

func TestModelsYAMLRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	src := `ollama_url: http://localhost:11434
models:
    open:
        overrides:
            llama_server:
                fast_light_text: phi4:14b
`
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, ok := cfg.Models.OverrideFor("llama_server", TierFastLightText); !ok || got != "phi4:14b" {
		t.Errorf("override = (%q,%v), want phi4:14b,true", got, ok)
	}
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, _ := os.ReadFile(path)
	for _, want := range []string{"models:", "open:", "overrides:", "llama_server:", "fast_light_text: phi4:14b"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("saved config missing %q:\n%s", want, out)
		}
	}
}

func TestApplyModelTierPatch(t *testing.T) {
	var m ModelsConfig

	desc, err := ApplyModelTierPatch(&m, "llama_server.fast_light_text", "phi4:14b")
	if err != nil {
		t.Fatalf("set slot: %v", err)
	}
	if got, ok := m.OverrideFor("llama_server", TierFastLightText); !ok || got != "phi4:14b" {
		t.Errorf("slot not set: (%q,%v)", got, ok)
	}
	if !strings.Contains(desc, "llama_server.fast_light_text") {
		t.Errorf("change description should name the slot, got %q", desc)
	}

	// Cloud is no longer a patchable provider side, and tier names are validated.
	if _, err := ApplyModelTierPatch(&m, "llama_server.cloud", "claude-opus"); err == nil {
		t.Error("cloud tier must be rejected")
	}

	if _, err := ApplyModelTierPatch(&m, "llama_server.fast_light_text", "-"); err != nil {
		t.Fatalf("clear slot: %v", err)
	}
	if got, ok := m.OverrideFor("llama_server", TierFastLightText); ok || got != "" {
		t.Errorf("slot not cleared: (%q,%v)", got, ok)
	}
	if len(m.Open.Overrides) != 0 {
		t.Errorf("empty runtime map should be pruned, got %#v", m.Open.Overrides)
	}

	if _, err := ApplyModelTierPatch(&m, "llama_server.medium_rare", "x"); err == nil {
		t.Error("unknown tier must be rejected")
	}
	if _, err := ApplyModelTierPatch(&m, "everyday", "x"); err == nil {
		t.Error("missing runtime/tier segment must be rejected")
	}
}

// TestVisionTierOverride covers the optional, override-only vision tier: it is
// a valid patch/override target and round-trips through OverrideFor, but is
// deliberately NOT one of any runtime's required tiers.
func TestVisionTierOverride(t *testing.T) {
	if !validTier(TierVision) {
		t.Fatal("vision must be a known tier")
	}

	var m ModelsConfig
	desc, err := ApplyModelTierPatch(&m, "llama_server.vision", "gemma-3-4b-it-q4_k_m")
	if err != nil {
		t.Fatalf("set vision slot: %v", err)
	}
	if !strings.Contains(desc, "llama_server.vision") {
		t.Errorf("change description should name the vision slot, got %q", desc)
	}
	if got, ok := m.OverrideFor("llama_server", TierVision); !ok || got != "gemma-3-4b-it-q4_k_m" {
		t.Errorf("vision override = (%q,%v), want gemma-3-4b-it-q4_k_m,true", got, ok)
	}

	// Unset vision is a normal miss, not an error condition.
	if got, ok := m.OverrideFor("mistralrs", TierVision); ok || got != "" {
		t.Errorf("unset vision override = (%q,%v), want empty,false", got, ok)
	}
}

func TestModelTierSlots(t *testing.T) {
	var m ModelsConfig
	m.SetOverride("llama_server", TierFastLightText, "phi4:14b")
	m.SetOverride("llama_server", TierEveryday, "qwen3-coder")
	m.SetOverride("mistralrs", TierEveryday, "mistral-qwen")

	slots := m.TierSlots()
	if len(slots) != 3 {
		t.Fatalf("slots = %v, want 3 entries", slots)
	}
	if slots["llama_server.fast_light_text"] != "phi4:14b" || slots["llama_server.everyday"] != "qwen3-coder" || slots["mistralrs.everyday"] != "mistral-qwen" {
		t.Errorf("slots = %v", slots)
	}
}
