package llamaserver

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestProfileEntry_UnmarshalString(t *testing.T) {
	var e ProfileEntry
	if err := json.Unmarshal([]byte(`"glm-4.5-air-q4_k_m"`), &e); err != nil {
		t.Fatal(err)
	}
	if e.Model != "glm-4.5-air-q4_k_m" || e.ContextSize != 0 {
		t.Fatalf("entry = %+v, want model only", e)
	}
}

func TestProfileEntry_UnmarshalObject(t *testing.T) {
	var e ProfileEntry
	if err := json.Unmarshal([]byte(`{"model":"glm-4.5-air-q4_k_m","ctx_size":131072}`), &e); err != nil {
		t.Fatal(err)
	}
	if e.Model != "glm-4.5-air-q4_k_m" || e.ContextSize != 131072 {
		t.Fatalf("entry = %+v", e)
	}
}

func TestValidateCatalog_ProfileEntryErrors(t *testing.T) {
	models := map[string]CuratedModel{
		"chatty":   {ID: "chatty", SupportsTools: true},
		"embedder": {ID: "embedder", SupportsEmbed: true},
	}
	base := func(entry ProfileEntry) CuratedCatalog {
		return CuratedCatalog{Models: models, Profiles: map[string]map[string]ProfileEntry{
			"48": {
				"most_capable":    entry,
				"everyday":        {Model: "chatty"},
				"fast_light":      {Model: "chatty"},
				"fast_light_text": {Model: "chatty"},
				"embedding":       {Model: "embedder"},
			},
		}}
	}
	cases := []struct {
		name  string
		entry ProfileEntry
		want  string
	}{
		{"missing model", ProfileEntry{}, "empty model"},
		{"unknown model", ProfileEntry{Model: "missing"}, "unknown model"},
		{"negative ctx", ProfileEntry{Model: "chatty", ContextSize: -1}, "invalid ctx_size"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCatalog(base(tc.entry))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateCatalog_RejectsInconsistentContextForSameModel(t *testing.T) {
	cat := CuratedCatalog{
		Models: map[string]CuratedModel{
			"chatty":   {ID: "chatty", SupportsTools: true},
			"embedder": {ID: "embedder", SupportsEmbed: true},
		},
		Profiles: map[string]map[string]ProfileEntry{
			"48": {
				"most_capable":    {Model: "chatty", ContextSize: 32768},
				"everyday":        {Model: "chatty", ContextSize: 65536},
				"fast_light":      {Model: "chatty", ContextSize: 32768},
				"fast_light_text": {Model: "chatty", ContextSize: 32768},
				"embedding":       {Model: "embedder", ContextSize: 8192},
			},
		},
	}
	if err := validateCatalog(cat); err == nil || !strings.Contains(err.Error(), "inconsistent ctx_size") {
		t.Fatalf("err = %v, want inconsistent ctx_size", err)
	}
}

func TestEmbeddedProfiles_ContextSizesDoNotExceedKnownTrainingContext(t *testing.T) {
	cat, err := loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	knownTrainCtx := map[string]int{
		"glm-4.5-air-q4_k_m":        131072,
		"gemma-3-4b-it-q4_k_m":      131072,
		"qwen3-14b-q4_k_m":          40960,
		"nomic-embed-text-v1.5-f16": 8192,
	}
	for profile, tiers := range cat.Profiles {
		for tier, entry := range tiers {
			limit, ok := knownTrainCtx[entry.Model]
			if !ok || entry.ContextSize <= 0 {
				continue
			}
			if entry.ContextSize > limit {
				t.Fatalf("profile %s tier %s model %s ctx_size=%d exceeds known training context %d", profile, tier, entry.Model, entry.ContextSize, limit)
			}
		}
	}
}

func TestEmbeddedProfiles_ContextSizes(t *testing.T) {
	cat, err := loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if keys := sortedProfileKeys(cat); strings.Join(keys, ",") != "24,48,96,128" {
		t.Fatalf("profile keys = %v, want existing 24/48/96/128", keys)
	}
	checks := []struct {
		profile string
		tier    string
		model   string
		ctx     int
	}{
		{"24", "most_capable", "qwen3-14b-q4_k_m", 32768},
		{"48", "most_capable", "glm-4.7-flash-q5_k_m", 32768},
		{"96", "most_capable", "glm-4.5-air-q4_k_m", 65536},
		{"128", "most_capable", "glm-4.5-air-q4_k_m", 131072},
		{"128", "embedding", "nomic-embed-text-v1.5-f16", 8192},
	}
	for _, check := range checks {
		entry := cat.Profiles[check.profile][check.tier]
		if entry.Model != check.model || entry.ContextSize != check.ctx {
			t.Fatalf("profile %s tier %s = %+v, want model=%s ctx=%d", check.profile, check.tier, entry, check.model, check.ctx)
		}
	}
	if args := cat.Models["glm-4.5-air-q4_k_m"].ExtraArgs; hasFlag(args, "--ctx-size") {
		t.Fatalf("GLM Air model extra_args must not carry RAM-tier ctx policy: %v", args)
	}
}

func sortedProfileKeys(cat CuratedCatalog) []string {
	keys := make([]string, 0, len(cat.Profiles))
	for k := range cat.Profiles {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		ki, _ := strconv.Atoi(keys[i])
		kj, _ := strconv.Atoi(keys[j])
		return ki < kj
	})
	return keys
}
