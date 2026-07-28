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
	// PlainChatOK reports whether the model produces correct visible assistant
	// content for ordinary (non-tool) chat on the pinned build. Absent means
	// true; it is only set false for models that load and can do tool calls but
	// return empty/garbled plain-chat content (e.g. GLM-4.5-Air on llama-server).
	// A false model must never be auto-selected for a plain-chat capability tier.
	PlainChatOK *bool `json:"plain_chat_ok,omitempty"`
	// Status is an authoring note: "tested" (default when empty), "experimental",
	// or "broken". Informational; PlainChatOK/SupportsTools are the enforced gates.
	Status string `json:"status,omitempty"`
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

// CuratedCatalog is the parsed catalog.json: a model dictionary keyed by ID
// plus RAM-tier profiles. A profile maps each capability tier to a model ID;
// profile keys are RAM thresholds in GB as strings ("24","48","96","128").
// Reusing IDs across profiles (the same Phi-4-mini serves every profile's fast
// tiers) keeps the data DRY.
type CuratedCatalog struct {
	Models   map[string]CuratedModel      `json:"models"`
	Profiles map[string]map[string]string `json:"profiles"`
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
		for _, tier := range requiredTiers {
			id, ok := prof[tier]
			if !ok || id == "" {
				return fmt.Errorf("profile %q is missing tier %q", name, tier)
			}
			model, ok := cat.Models[id]
			if !ok {
				return fmt.Errorf("profile %q tier %q references unknown model %q", name, tier, id)
			}
			// Every required tier except embedding is a plain-chat tier: it must
			// reference a model that actually produces visible chat content, or
			// the wizard would recommend a model that answers with empty output.
			if tier != "embedding" && !model.PlainChatSupported() {
				return fmt.Errorf("profile %q tier %q references model %q which is not plain-chat capable (plain_chat_ok:false)", name, tier, id)
			}
		}
	}
	return nil
}

// ProfileForRAM picks the profile for a machine with the given total RAM: the
// largest profile whose GB threshold is at or below the machine's memory, so a
// 64 GB box takes the 48 GB profile and a 128 GB box takes 128. A machine
// below the smallest threshold still gets the smallest profile (better a tight
// fit than nothing). Returns the tier→modelID map and the chosen threshold.
func (c CuratedCatalog) ProfileForRAM(totalBytes uint64) (map[string]string, int) {
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
