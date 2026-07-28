package mistralrs

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"cercano/source/server/internal/mistralrscompat"
)

// catalogJSON is the curated mistral.rs catalog — RAM-tiered chat models
// (UQFF, or safetensors for the smaller ones) verified to load on the pinned
// mistral.rs build. It mirrors the llama-server curated catalog, with two
// differences: entries carry a Format (uqff|safetensors|gguf), and the profiles
// fill only the chat tiers — mistral.rs's loaders are text-generation only, so
// the embedding tier stays cross-runtime (the shared nomic served by Ollama).
//
//go:embed catalog.json
var catalogJSON []byte

// CuratedModel is one curated mistral.rs model. Files is the full download
// manifest: for UQFF, the chosen quant .uqff plus residual.safetensors and the
// config/tokenizer sidecars; for safetensors, the weight shards plus config and
// tokenizer. SizeBytes is the sum across files. Arch is mistral.rs's model_type
// (the mistralrscompat gate input).
type CuratedModel struct {
	ID            string   `json:"id"`
	DisplayName   string   `json:"display_name"`
	Repo          string   `json:"repo"`
	Files         []string `json:"files"`
	Format        string   `json:"format"` // "uqff" | "safetensors" | "gguf"
	Architecture  string   `json:"arch"`
	Family        string   `json:"family"`
	SizeBytes     int64    `json:"size_bytes"`
	SupportsTools bool     `json:"supports_tools"`
	// PlainChatOK reports whether the model produces correct visible assistant
	// content for ordinary (non-tool) chat on the pinned build. Absent means
	// true; set false only for models that load and can do tool calls but return
	// empty/garbled plain-chat content. A false model must never be
	// auto-selected for a plain-chat capability tier.
	PlainChatOK *bool `json:"plain_chat_ok,omitempty"`
	// Status is an authoring note: "tested" (default when empty), "experimental",
	// or "broken". Informational.
	Status string `json:"status,omitempty"`
}

// PlainChatSupported reports whether the model is safe for plain-chat tiers.
// Absent PlainChatOK (nil) defaults to true.
func (m CuratedModel) PlainChatSupported() bool {
	return m.PlainChatOK == nil || *m.PlainChatOK
}

// DownloadURLs returns the HuggingFace resolve URL for each manifest file.
func (m CuratedModel) DownloadURLs() []string {
	out := make([]string, 0, len(m.Files))
	for _, f := range m.Files {
		out = append(out, "https://huggingface.co/"+m.Repo+"/resolve/main/"+f)
	}
	return out
}

// CuratedCatalog is the parsed catalog.json: a model dictionary keyed by ID
// plus RAM-tier profiles (keys are GB thresholds as strings). Each profile
// fills the chat tiers; embedding is not a mistral.rs tier.
type CuratedCatalog struct {
	Models   map[string]CuratedModel      `json:"models"`
	Profiles map[string]map[string]string `json:"profiles"`
}

// requiredTiers are the chat tiers every mistral.rs profile must fill. Unlike
// llama-server there is no embedding tier — mistral.rs doesn't serve embeddings,
// so that recommendation stays on the shared cross-runtime model.
var requiredTiers = []string{"most_capable", "everyday", "fast_light", "fast_light_text"}

// loadCatalog parses the embedded catalog.json and checks referential
// integrity: at least one profile, every profile fills every required chat
// tier, every referenced model exists, and every model has an id/repo/files, a
// known format, and an architecture mistral.rs can load. A bad entry fails the
// build via the validity test, not a user's setup.
func loadCatalog() (CuratedCatalog, error) {
	var cat CuratedCatalog
	if err := json.Unmarshal(catalogJSON, &cat); err != nil {
		return CuratedCatalog{}, fmt.Errorf("parse catalog.json: %w", err)
	}
	for id, m := range cat.Models {
		if m.ID == "" || m.Repo == "" || len(m.Files) == 0 {
			return CuratedCatalog{}, fmt.Errorf("catalog model %q missing id/repo/files", id)
		}
		switch m.Format {
		case "uqff", "safetensors", "gguf":
		default:
			return CuratedCatalog{}, fmt.Errorf("catalog model %q has unknown format %q", id, m.Format)
		}
		if !mistralrscompat.Supported(m.Architecture) {
			return CuratedCatalog{}, fmt.Errorf("catalog model %q architecture %q not loadable by mistral.rs", id, m.Architecture)
		}
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
			model, ok := cat.Models[id]
			if !ok {
				return CuratedCatalog{}, fmt.Errorf("profile %q tier %q references unknown model %q", name, tier, id)
			}
			// All mistral.rs required tiers are plain-chat tiers (no embedding
			// tier here), so each must reference a plain-chat-capable model.
			if !model.PlainChatSupported() {
				return CuratedCatalog{}, fmt.Errorf("profile %q tier %q references model %q which is not plain-chat capable (plain_chat_ok:false)", name, tier, id)
			}
		}
	}
	return cat, nil
}

// ProfileForRAM picks the profile for a machine with the given total RAM: the
// largest profile whose GB threshold is at or below the machine's memory. A
// machine below the smallest threshold still gets the smallest profile.
func (c CuratedCatalog) ProfileForRAM(totalBytes uint64) (map[string]string, int) {
	gb := int(totalBytes / (1024 * 1024 * 1024))
	thresholds := make([]int, 0, len(c.Profiles))
	for k := range c.Profiles {
		if n, err := strconv.Atoi(k); err == nil {
			thresholds = append(thresholds, n)
		}
	}
	sort.Ints(thresholds)
	if len(thresholds) == 0 {
		return nil, 0
	}
	chosen := thresholds[0]
	for _, t := range thresholds {
		if gb >= t {
			chosen = t
		}
	}
	return c.Profiles[strconv.Itoa(chosen)], chosen
}

// RecommendedOpenModels returns the curated mistral.rs chat-tier recommendation
// for a machine with the given total RAM: tier -> the stable inventory id
// "mistralrs:catalog:<bareID>" that catalogModels() surfaces. The embedding
// tier is intentionally absent (mistral.rs doesn't serve embeddings); the
// caller keeps embedding on the shared cross-runtime model. Nil when the
// embedded catalog fails to load (a build-time bug the validity test guards).
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
