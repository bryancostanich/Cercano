package config

import (
	"fmt"
	"strings"
)

// Tier names a capability class in the model taxonomy. Consumers ask for a
// tier, not a model id — which model serves a tier is configuration.
type Tier string

const (
	TierMostCapable Tier = "most_capable" // frontier reasoning for hard tasks
	TierEveryday    Tier = "everyday"     // default workhorse for main chat
	TierFastLight   Tier = "fast_light"   // small/low-latency background helpers
	// TierFastLightText is fast_light for prose-quality judgment work —
	// watchdog verdicts, summaries, recaps. Distinct from fast_light because
	// small coder models are poor text judges and small text models are poor
	// code helpers.
	TierFastLightText Tier = "fast_light_text"
	// TierEmbedding is the embedding model slot. Only the open side is
	// meaningful today (embeddings run on the configured local runtime);
	// the cloud side exists for shape-consistency and future use.
	TierEmbedding Tier = "embedding"
)

// Provider names which side of the taxonomy a model id lives on. "Open" names
// the weights family (open-weight models we run ourselves — today via local
// Ollama, but the name survives running them on a remote box). "Cloud" is a
// hosted API service.
type Provider string

const (
	ProviderCloud Provider = "cloud"
	ProviderOpen  Provider = "open"
)

// ModelTier holds the open-side model id for one tier. Empty = not configured.
//
// Cloud is NOT represented here: cloud models are vendor-owned and resolve
// through the vendor-keyed cost-tier path (ModelProfiles.Cloud.Providers +
// ResolveCloudModelForTier), not through capability-tier slots. The retired
// four-tier `cloud` slot was deleted (effort: runtime-aware-model-tiers,
// Phase 2) so a fourth cloud tier can never reappear here.
type ModelTier struct {
	Open string `yaml:"open"`
}

// ModelTiers is the full tier table.
type ModelTiers struct {
	MostCapable   ModelTier `yaml:"most_capable"`
	Everyday      ModelTier `yaml:"everyday"`
	FastLight     ModelTier `yaml:"fast_light"`
	FastLightText ModelTier `yaml:"fast_light_text"`
	Embedding     ModelTier `yaml:"embedding,omitempty"`
}

// ModelsConfig is the model taxonomy: which model serves each capability tier,
// per provider side. The everyday tier's slots deliberately have NO copy-based
// migration from the legacy cloud-profile/open_model fields — an empty
// everyday slot falls through to those live values at resolution time (see
// Server.resolveTierModel), so there is exactly one source of truth and no
// stale mirror to drift.
type ModelsConfig struct {
	DefaultProvider Provider   `yaml:"default_provider"` // side to prefer when the caller has no preference
	Tiers           ModelTiers `yaml:"tiers"`
}

// OpenChatModel resolves the interactive local chat model — the everyday
// tier's open slot. This is THE way to read "which local model do I chat
// with"; the legacy open_model key is retired (migrated at load, dropped
// from Save; design: docs/features/local-model-taxonomy/design.md).
func (c *Config) OpenChatModel() string {
	return c.Models.Tiers.Everyday.Open
}

// OpenEmbeddingModel resolves the embedding model the same way — the
// embedding tier's open slot (legacy embedding_model key retired).
func (c *Config) OpenEmbeddingModel() string {
	return c.Models.Tiers.Embedding.Open
}

// tier returns the ModelTier for t (zero value for unknown tiers).
func (m ModelsConfig) tier(t Tier) ModelTier {
	switch t {
	case TierMostCapable:
		return m.Tiers.MostCapable
	case TierEveryday:
		return m.Tiers.Everyday
	case TierFastLight:
		return m.Tiers.FastLight
	case TierFastLightText:
		return m.Tiers.FastLightText
	case TierEmbedding:
		return m.Tiers.Embedding
	}
	return ModelTier{}
}

// tierSlot returns a pointer to the named tier's struct, or nil for unknown.
func (m *ModelsConfig) tierSlot(t Tier) *ModelTier {
	switch t {
	case TierMostCapable:
		return &m.Tiers.MostCapable
	case TierEveryday:
		return &m.Tiers.Everyday
	case TierFastLight:
		return &m.Tiers.FastLight
	case TierFastLightText:
		return &m.Tiers.FastLightText
	case TierEmbedding:
		return &m.Tiers.Embedding
	}
	return nil
}

// ApplyModelTierPatch applies one sparse-patch update to the taxonomy:
// key "default_provider" sets the preferred side (cloud|open); key
// "<tier>.<provider>" sets that slot's model id, with "-" clearing it.
// Returns a short change description for the caller's change log.
func ApplyModelTierPatch(m *ModelsConfig, key, value string) (string, error) {
	if key == "default_provider" {
		p := Provider(value)
		if p != ProviderCloud && p != ProviderOpen {
			return "", fmt.Errorf("models.default_provider must be %q or %q, got %q", ProviderCloud, ProviderOpen, value)
		}
		m.DefaultProvider = p
		return "models.default_provider=" + value, nil
	}
	tierName, provName, ok := strings.Cut(key, ".")
	if !ok {
		return "", fmt.Errorf("model tier key %q must be \"default_provider\" or \"<tier>.<provider>\"", key)
	}
	slot := m.tierSlot(Tier(tierName))
	if slot == nil {
		return "", fmt.Errorf("unknown model tier %q (want %s|%s|%s|%s|%s)", tierName,
			TierMostCapable, TierEveryday, TierFastLight, TierFastLightText, TierEmbedding)
	}
	if value == "-" {
		value = ""
	}
	switch Provider(provName) {
	case ProviderOpen:
		slot.Open = value
	default:
		// Cloud is configured via its own vendor-keyed profile path, not
		// through capability-tier slots; only the open side is patchable here.
		return "", fmt.Errorf("unknown provider %q in model tier key (want %s)", provName, ProviderOpen)
	}
	shown := value
	if shown == "" {
		shown = "-"
	}
	return "models." + key + "=" + shown, nil
}

// TierSlots enumerates the non-empty tier slots keyed "<tier>.<provider>" —
// the read-side view served by GetConfig and rendered by /config show.
func (m ModelsConfig) TierSlots() map[string]string {
	out := map[string]string{}
	for _, t := range []Tier{TierMostCapable, TierEveryday, TierFastLight, TierFastLightText, TierEmbedding} {
		mt := m.tier(t)
		if mt.Open != "" {
			out[string(t)+".open"] = mt.Open
		}
	}
	return out
}

// ResolveOpen returns the configured open (local) model id for a tier, and
// ok=false when nothing is configured — the caller decides what that means (a
// background helper skips; main chat errors). There is no provider preference
// or cross-provider fallback: cloud is resolved through its own vendor-keyed
// cost-tier path (ModelProfiles.ResolveCloudModelForTier), so a tier only ever
// yields its single open slot.
func (m ModelsConfig) ResolveOpen(t Tier) (string, bool) {
	if id := m.tier(t).Open; id != "" {
		return id, true
	}
	return "", false
}
