package config

import (
	"os"
	"path/filepath"
	"strings"
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

// TestTierDefaultsFillEmptySlots: with no model keys in the FILE, the tier
// slots get the stock defaults — and the retired legacy fields stay blank,
// so a default can never masquerade as a user's migrated choice.
func TestTierDefaultsFillEmptySlots(t *testing.T) {
	cfg := loadFromYAML(t, `
port: "50052"
`)
	if got := cfg.Models.Tiers.Everyday.Open; got != "qwen3-coder" {
		t.Errorf("everyday.open = %q, want stock default", got)
	}
	if got := cfg.Models.Tiers.Embedding.Open; got != "nomic-embed-text" {
		t.Errorf("embedding.open = %q, want stock default", got)
	}
	if cfg.OpenModel != "" || cfg.EmbeddingModel != "" {
		t.Errorf("legacy fields = (%q, %q), want blanked after load", cfg.OpenModel, cfg.EmbeddingModel)
	}
}

// TestRetiredKeysDroppedOnSave: a migrated config saves WITHOUT the legacy
// keys — the tier slots carry the values from then on.
func TestRetiredKeysDroppedOnSave(t *testing.T) {
	cfg := loadFromYAML(t, `
open_model: my-chat-model
embedding_model: my-embed-model
`)
	out := filepath.Join(t.TempDir(), "saved.yaml")
	if err := Save(cfg, out); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "open_model:") || strings.Contains(text, "embedding_model:") {
		t.Errorf("saved config still contains retired keys:\n%s", text)
	}
	reloaded, err := Load(out)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.Models.Tiers.Everyday.Open; got != "my-chat-model" {
		t.Errorf("reloaded everyday.open = %q, want migrated value to survive the round trip", got)
	}
	if got := reloaded.Models.Tiers.Embedding.Open; got != "my-embed-model" {
		t.Errorf("reloaded embedding.open = %q", got)
	}
}

// TestEnvOverrideLandsInTier: the CERCANO_OPEN_MODEL / CERCANO_EMBEDDING_MODEL
// env overrides write the tier slots (and win over the file).
func TestEnvOverrideLandsInTier(t *testing.T) {
	t.Setenv("CERCANO_OPEN_MODEL", "env-chat")
	t.Setenv("CERCANO_EMBEDDING_MODEL", "env-embed")
	cfg := loadFromYAML(t, `
open_model: file-chat
`)
	if got := cfg.Models.Tiers.Everyday.Open; got != "env-chat" {
		t.Errorf("everyday.open = %q, want env override to win", got)
	}
	if got := cfg.Models.Tiers.Embedding.Open; got != "env-embed" {
		t.Errorf("embedding.open = %q, want env override", got)
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
