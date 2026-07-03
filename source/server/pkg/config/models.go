package config

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

// ModelTier holds the per-provider model ids for one tier. Empty = not
// configured on that side.
type ModelTier struct {
	Cloud string `yaml:"cloud"`
	Open  string `yaml:"open"`
}

// ModelTiers is the full tier table.
type ModelTiers struct {
	MostCapable   ModelTier `yaml:"most_capable"`
	Everyday      ModelTier `yaml:"everyday"`
	FastLight     ModelTier `yaml:"fast_light"`
	FastLightText ModelTier `yaml:"fast_light_text"`
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
	}
	return ModelTier{}
}

// side returns the model id for one provider side of a tier.
func (mt ModelTier) side(p Provider) string {
	if p == ProviderCloud {
		return mt.Cloud
	}
	return mt.Open
}

// other returns the opposite provider side.
func (p Provider) other() Provider {
	if p == ProviderCloud {
		return ProviderOpen
	}
	return ProviderCloud
}

// Resolve returns the configured model for a tier. prefer picks the provider
// side; empty prefer falls back to DefaultProvider (then open, the local-first
// default). When the preferred side is empty and strict is false, the other
// side is tried. Returns ok=false when nothing is configured — the caller
// decides what that means (a background helper skips; main chat errors).
func (m ModelsConfig) Resolve(t Tier, prefer Provider, strict bool) (string, Provider, bool) {
	p := prefer
	if p == "" {
		p = m.DefaultProvider
	}
	if p == "" {
		p = ProviderOpen
	}
	mt := m.tier(t)
	if id := mt.side(p); id != "" {
		return id, p, true
	}
	if strict {
		return "", "", false
	}
	if id := mt.side(p.other()); id != "" {
		return id, p.other(), true
	}
	return "", "", false
}
