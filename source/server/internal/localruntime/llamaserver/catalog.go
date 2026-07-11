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
// with pkg/config's ModelTiers; a profile missing any of these is a catalog
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
	if len(cat.Profiles) == 0 {
		return CuratedCatalog{}, fmt.Errorf("catalog has no profiles")
	}
	for name, prof := range cat.Profiles {
		for _, tier := range requiredTiers {
			id, ok := prof[tier]
			if !ok || id == "" {
				return CuratedCatalog{}, fmt.Errorf("profile %q is missing tier %q", name, tier)
			}
			if _, ok := cat.Models[id]; !ok {
				return CuratedCatalog{}, fmt.Errorf("profile %q tier %q references unknown model %q", name, tier, id)
			}
		}
	}
	return cat, nil
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
