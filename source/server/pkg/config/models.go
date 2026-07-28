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

// OpenModels holds the open (local) model taxonomy as per-runtime OVERRIDES:
// runtime name → tier name → model id. It stores ONLY the tiers the user has
// explicitly customized. Anything not present here resolves to that runtime's
// curated catalog default — which lives in internal/localruntime and is merged
// in by the server (Server.ResolveOpenModel), never by pkg/config (which must
// not import the catalog). So config is override-only and can never hold a
// stale copy of a default: untouched tiers always track the catalog.
//
// Overrides are per-runtime and never ported across runtimes — a model id that
// is valid for llama_server may be meaningless or invalid on mistralrs.
type OpenModels struct {
	// Overrides[runtime][tier] = model id. Both levels are sparse.
	Overrides map[string]map[string]string `yaml:"overrides,omitempty"`
}

// ModelsConfig is the model taxonomy. The open side is per-runtime overrides
// over the catalog default (see OpenModels); the cloud side resolves through
// its own vendor-keyed cost-tier path (ModelProfiles.Cloud.Providers +
// ResolveCloudModelForTier) and is not represented here.
type ModelsConfig struct {
	Open OpenModels `yaml:"open"`
}

// OverrideFor returns the user's override model id for (runtime, tier), and
// ok=false when there is none. ok=false is EXPECTED and not an error — it means
// "use the catalog default", which only the server can compute. This is the
// only open-model lookup pkg/config exposes; the effective running model
// (override-else-catalog-default) is resolved server-side.
func (m ModelsConfig) OverrideFor(runtime string, t Tier) (string, bool) {
	tiers, ok := m.Open.Overrides[runtime]
	if !ok {
		return "", false
	}
	id, ok := tiers[string(t)]
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

// SetOverride writes (or, with an empty id, clears) the override for
// (runtime, tier). Clearing prunes empty maps so config stays minimal and a
// runtime with no customizations leaves no residue.
func (m *ModelsConfig) SetOverride(runtime string, t Tier, id string) {
	if id == "" {
		if tiers, ok := m.Open.Overrides[runtime]; ok {
			delete(tiers, string(t))
			if len(tiers) == 0 {
				delete(m.Open.Overrides, runtime)
			}
		}
		return
	}
	if m.Open.Overrides == nil {
		m.Open.Overrides = map[string]map[string]string{}
	}
	if m.Open.Overrides[runtime] == nil {
		m.Open.Overrides[runtime] = map[string]string{}
	}
	m.Open.Overrides[runtime][string(t)] = id
}

// ApplyModelTierPatch applies one sparse-patch update to the open taxonomy:
// key "<runtime>.<tier>" sets that runtime's override for the tier, with "-"
// clearing it. The runtime is explicit so setup can seed a runtime you are
// about to switch to. (Cloud is configured via its own vendor-keyed profile
// path, not here.) Returns a short change description for the caller's log.
func ApplyModelTierPatch(m *ModelsConfig, key, value string) (string, error) {
	runtimeName, tierName, ok := strings.Cut(key, ".")
	if !ok {
		return "", fmt.Errorf("model tier key %q must be \"<runtime>.<tier>\"", key)
	}
	if !validTier(Tier(tierName)) {
		return "", fmt.Errorf("unknown model tier %q (want %s|%s|%s|%s|%s)", tierName,
			TierMostCapable, TierEveryday, TierFastLight, TierFastLightText, TierEmbedding)
	}
	if runtimeName == "" {
		return "", fmt.Errorf("model tier key %q is missing a runtime", key)
	}
	if value == "-" {
		value = ""
	}
	m.SetOverride(runtimeName, Tier(tierName), value)
	shown := value
	if shown == "" {
		shown = "-"
	}
	return "models.open.overrides." + key + "=" + shown, nil
}

// TierSlots enumerates all override slots keyed "<runtime>.<tier>" — the
// read-side view served by GetConfig and rendered by /config show.
func (m ModelsConfig) TierSlots() map[string]string {
	out := map[string]string{}
	for runtime, tiers := range m.Open.Overrides {
		for tier, id := range tiers {
			if id != "" {
				out[runtime+"."+tier] = id
			}
		}
	}
	return out
}

// validTier reports whether t is a known tier name.
func validTier(t Tier) bool {
	switch t {
	case TierMostCapable, TierEveryday, TierFastLight, TierFastLightText, TierEmbedding:
		return true
	}
	return false
}
