package config

import (
	"os"
	"path/filepath"
	"testing"
)

func loadFromYAML(t *testing.T, yaml string) Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

// TestMigrateModelTiers_SeedsFromLegacyKeys: a config with only the legacy
// standalone keys populates the everyday.open and embedding.open tier slots.
func TestMigrateModelTiers_SeedsFromLegacyKeys(t *testing.T) {
	cfg := loadFromYAML(t, `
open_model: qwen3-coder-next
embedding_model: nomic-embed-text
`)
	if got := cfg.Models.Tiers.Everyday.Open; got != "qwen3-coder-next" {
		t.Errorf("everyday.open = %q, want seeded from open_model", got)
	}
	if got := cfg.Models.Tiers.Embedding.Open; got != "nomic-embed-text" {
		t.Errorf("embedding.open = %q, want seeded from embedding_model", got)
	}
}

// TestMigrateModelTiers_TierSlotWins: a file that assigns the tier slot
// directly beats the legacy key.
func TestMigrateModelTiers_TierSlotWins(t *testing.T) {
	cfg := loadFromYAML(t, `
open_model: old-model
models:
    tiers:
        everyday:
            open: chosen-model
`)
	if got := cfg.Models.Tiers.Everyday.Open; got != "chosen-model" {
		t.Errorf("everyday.open = %q, want the explicit tier assignment to win", got)
	}
}

// TestMigrateModelTiers_DefaultsDoNotLeak: with no legacy keys in the FILE,
// the tier slots stay empty even though Defaults() back-fills the legacy
// fields after migration — a defaulted model must not masquerade as a user's
// tier choice.
func TestMigrateModelTiers_DefaultsDoNotLeak(t *testing.T) {
	cfg := loadFromYAML(t, `
port: "50052"
`)
	if cfg.OpenModel == "" {
		t.Fatal("precondition: defaults should back-fill the legacy OpenModel field")
	}
	if got := cfg.Models.Tiers.Everyday.Open; got != "" {
		t.Errorf("everyday.open = %q, want empty (defaults must not leak into tiers)", got)
	}
	if got := cfg.Models.Tiers.Embedding.Open; got != "" {
		t.Errorf("embedding.open = %q, want empty", got)
	}
}

// TestTierSlots_IncludesEmbedding: the read-side view serves the new slot.
func TestTierSlots_IncludesEmbedding(t *testing.T) {
	var m ModelsConfig
	if _, err := ApplyModelTierPatch(&m, "embedding.open", "nomic-embed-text"); err != nil {
		t.Fatalf("ApplyModelTierPatch(embedding.open): %v", err)
	}
	if got := m.TierSlots()["embedding.open"]; got != "nomic-embed-text" {
		t.Errorf("TierSlots()[embedding.open] = %q", got)
	}
	if id, _, ok := m.Resolve(TierEmbedding, ProviderOpen, true); !ok || id != "nomic-embed-text" {
		t.Errorf("Resolve(embedding, open) = (%q, ok=%v)", id, ok)
	}
}
