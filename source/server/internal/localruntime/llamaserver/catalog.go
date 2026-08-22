package llamaserver

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// catalogJSON is the curated compatibility catalog — the RAM-tiered set of
// GGUFs we have verified load on the pinned llama.cpp build. It is the only
// model source the setup wizard touches, which is what makes the guided path
// structurally foolproof (nothing incompatible can be recommended). Browsing
// arbitrary HuggingFace GGUFs is a separate, gated path.
//
//go:embed catalog.json
var catalogJSON []byte

// CuratedModel is one GGUF in the curated catalog — a model we have actually
// run on the pinned build. Files holds a single path for a normal model or
// several for a sharded split (llama-server loads shard 1 and pulls the rest);
// a model counts as downloaded only when every file is present, and SizeBytes
// is the sum across shards.
type CuratedModel struct {
	ID            string   `json:"id"`
	DisplayName   string   `json:"display_name"`
	Repo          string   `json:"repo"`
	Files         []string `json:"files"`
	Architecture  string   `json:"arch"`
	Quantization  string   `json:"quant"`
	Family        string   `json:"family"`
	SizeBytes     int64    `json:"size_bytes"`
	SupportsTools bool     `json:"supports_tools"`
	SupportsEmbed bool     `json:"supports_embed"`
	// SupportsVision reports whether the model can accept image input. It is
	// true only for a multimodal model whose projector (MmprojFile) is also
	// present, since llama-server needs --mmproj to decode images. Curated
	// truth, exactly like SupportsTools — not inferred. A false model has its
	// images stripped before the request reaches the backend.
	SupportsVision bool `json:"supports_vision,omitempty"`
	// MmprojFile is the multimodal projector GGUF filename for a vision model,
	// and MUST also appear in Files so the download manager fetches it. It is
	// passed to llama-server as --mmproj <path>. Empty for non-vision models.
	MmprojFile string `json:"mmproj_file,omitempty"`
	// PlainChatOK reports whether the model produces correct visible assistant
	// content for ordinary (non-tool) chat on the pinned build. Absent means
	// true; it is only set false for models that load and can do tool calls but
	// return empty/garbled plain-chat content (e.g. GLM-4.5-Air on llama-server).
	// A false model must never be auto-selected for a plain-chat capability tier.
	PlainChatOK *bool `json:"plain_chat_ok,omitempty"`
	// Status is an authoring note: "tested" (default when empty), "experimental",
	// or "broken". Informational; PlainChatOK/SupportsTools are the enforced gates.
	Status string `json:"status,omitempty"`
	// ExtraArgs are per-model llama-server launch flags appended after the
	// global config ExtraArgs. Use this only for flags a specific model
	// requires to run correctly — e.g. GLM-4.5-Air needs "--jinja" to activate
	// its native chat template; without it llama-server compute-fails at decode.
	// Scoped to the one model that needs it so unrelated models (Qwen, Phi) are
	// never affected.
	ExtraArgs []string `json:"extra_args,omitempty"`
}

// PlainChatSupported reports whether the model is safe for plain-chat tiers.
// Absent PlainChatOK (nil) defaults to true so only known-bad models must be
// annotated.
func (m CuratedModel) PlainChatSupported() bool {
	return m.PlainChatOK == nil || *m.PlainChatOK
}

// DownloadURLs returns the HuggingFace resolve URL for each file, in shard
// order. These are plain HTTPS GETs (Range-resumable) — no OCI, no manifest
// step — so the existing download path consumes them directly.
func (m CuratedModel) DownloadURLs() []string {
	out := make([]string, 0, len(m.Files))
	for _, f := range m.Files {
		out = append(out, "https://huggingface.co/"+m.Repo+"/resolve/main/"+f)
	}
	return out
}

// ProfileEntry maps a capability tier in a RAM profile to a catalog model and
// optional launch policy. ContextSize is a profile-level context-window
// override: the same model may run with different windows on 96 GB and 128 GB
// machines. Zero means no profile override (legacy/default behavior).
type ProfileEntry struct {
	Model       string `json:"model"`
	ContextSize int    `json:"ctx_size,omitempty"`
}

// UnmarshalJSON keeps the profile schema backward-compatible: an old string
// value means just the model ID, while the object form can carry ctx_size.
func (e *ProfileEntry) UnmarshalJSON(data []byte) error {
	var id string
	if err := json.Unmarshal(data, &id); err == nil {
		e.Model = id
		e.ContextSize = 0
		return nil
	}
	var obj struct {
		Model       string `json:"model"`
		ContextSize int    `json:"ctx_size"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	e.Model = obj.Model
	e.ContextSize = obj.ContextSize
	return nil
}

// CuratedCatalog is the parsed catalog.json: a model dictionary keyed by ID
// plus RAM-tier profiles. A profile maps each capability tier to a model entry;
// profile keys are RAM thresholds in GB as strings ("24","48","96","128").
// Reusing IDs across profiles keeps the data DRY; profile entries carry launch
// policy such as ctx_size when a RAM tier should run the same model differently.
type CuratedCatalog struct {
	Models   map[string]CuratedModel            `json:"models"`
	Profiles map[string]map[string]ProfileEntry `json:"profiles"`
}

// requiredTiers are the capability tiers every profile must fill. Kept in sync
// with pkg/config's Tier constants; a profile missing any of these is a catalog
// authoring bug caught at load time.
var requiredTiers = []string{"most_capable", "everyday", "fast_light", "fast_light_text", "embedding"}

// loadCatalog parses the embedded catalog.json and checks referential
// integrity: at least one profile, every profile fills every required tier,
// and every referenced model ID exists. It does NOT check architecture
// compatibility — that is the gate's concern, asserted by the catalog validity
// test against llamacompat so a bad entry fails the build, not a user's setup.
func loadCatalog() (CuratedCatalog, error) {
	var cat CuratedCatalog
	if err := json.Unmarshal(catalogJSON, &cat); err != nil {
		return CuratedCatalog{}, fmt.Errorf("parse catalog.json: %w", err)
	}
	if err := validateCatalog(cat); err != nil {
		return CuratedCatalog{}, err
	}
	return cat, nil
}

// validateCatalog checks referential integrity and capability gates for a
// parsed catalog: at least one profile, every profile fills every required
// tier, referenced models exist, and every chat (non-embedding) tier references
// a plain-chat-capable model. Split out from loadCatalog so tests can exercise
// the rules against synthetic catalogs.
func validateCatalog(cat CuratedCatalog) error {
	if len(cat.Profiles) == 0 {
		return fmt.Errorf("catalog has no profiles")
	}
	for name, prof := range cat.Profiles {
		ctxByModel := map[string]int{}
		for tier, entry := range prof {
			if entry.Model == "" {
				return fmt.Errorf("profile %q tier %q has empty model", name, tier)
			}
			if entry.ContextSize < 0 {
				return fmt.Errorf("profile %q tier %q has invalid ctx_size %d", name, tier, entry.ContextSize)
			}
			if entry.ContextSize > 0 {
				if prev := ctxByModel[entry.Model]; prev > 0 && prev != entry.ContextSize {
					return fmt.Errorf("profile %q model %q has inconsistent ctx_size values %d and %d", name, entry.Model, prev, entry.ContextSize)
				}
				ctxByModel[entry.Model] = entry.ContextSize
			}
			if _, ok := cat.Models[entry.Model]; !ok {
				return fmt.Errorf("profile %q tier %q references unknown model %q", name, tier, entry.Model)
			}
		}
		for _, tier := range requiredTiers {
			entry, ok := prof[tier]
			if !ok || entry.Model == "" {
				return fmt.Errorf("profile %q is missing tier %q", name, tier)
			}
			model := cat.Models[entry.Model]
			// Every required tier except embedding is a plain-chat tier: it must
			// reference a model that actually produces visible chat content, or
			// the wizard would recommend a model that answers with empty output.
			if tier != "embedding" && !model.PlainChatSupported() {
				return fmt.Errorf("profile %q tier %q references model %q which is not plain-chat capable (plain_chat_ok:false)", name, tier, entry.Model)
			}
		}
	}
	return nil
}

// ProfileForRAMEntries picks the profile for a machine with the given total
// RAM: the largest profile whose GB threshold is at or below the machine's
// memory, so a 64 GB box takes the 48 GB profile and a 128 GB box takes 128. A
// machine below the smallest threshold still gets the smallest profile (better
// a tight fit than nothing). Returns the tier→ProfileEntry map and the chosen
// threshold.
func (c CuratedCatalog) ProfileForRAMEntries(totalBytes uint64) (map[string]ProfileEntry, int) {
	gb := int(totalBytes / (1024 * 1024 * 1024))
	thresholds := make([]int, 0, len(c.Profiles))
	for k := range c.Profiles {
		if n, err := strconv.Atoi(k); err == nil {
			thresholds = append(thresholds, n)
		}
	}
	sort.Ints(thresholds)
	chosen := thresholds[0]
	for _, t := range thresholds {
		if gb >= t {
			chosen = t
		}
	}
	return c.Profiles[strconv.Itoa(chosen)], chosen
}

// ProfileForRAM preserves the older helper shape for callers that only need
// tier→modelID recommendations.
func (c CuratedCatalog) ProfileForRAM(totalBytes uint64) (map[string]string, int) {
	entries, chosen := c.ProfileForRAMEntries(totalBytes)
	out := make(map[string]string, len(entries))
	for tier, entry := range entries {
		out[tier] = entry.Model
	}
	return out, chosen
}

// RecommendedOpenModels returns the curated open-model recommendation for a
// machine with the given total RAM: tier → the stable inventory id
// "llama_server:catalog:<bareID>" that catalogModels() surfaces and
// localruntime.MatchesModel resolves both before and after download. The
// wizard autofills its open tier picks from this, so every recommendation is a
// gate-verified model that cannot be incompatible. Nil when the embedded
// catalog fails to load (a build-time bug the validity test guards).
func RecommendedOpenModels(totalBytes uint64) map[string]string {
	cat, err := loadCatalog()
	if err != nil {
		return nil
	}
	profile, _ := cat.ProfileForRAM(totalBytes)
	out := make(map[string]string, len(profile))
	for tier, bareID := range profile {
		out[tier] = runtimeName + ":catalog:" + bareID
	}
	return out
}
