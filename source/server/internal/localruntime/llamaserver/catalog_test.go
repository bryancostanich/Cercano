package llamaserver

import (
	"testing"

	"cercano/source/server/internal/llamacompat"
)

// TestLoadCatalog_Valid is the referential-integrity gate: the embedded
// catalog.json parses and every profile fills every required tier with a model
// that actually exists. A failure here means the shipped data file is
// malformed — caught at build time, never in a user's setup.
func TestLoadCatalog_Valid(t *testing.T) {
	cat, err := loadCatalog()
	if err != nil {
		t.Fatalf("loadCatalog: %v", err)
	}
	if len(cat.Models) == 0 {
		t.Fatal("catalog has no models")
	}
	if len(cat.Profiles) == 0 {
		t.Fatal("catalog has no profiles")
	}
}

// TestCatalog_AllArchesSupported is THE compatibility guarantee: every model
// any profile can recommend must have an architecture the pinned llama.cpp
// build can load. This is what makes the curated set structurally foolproof —
// the gate that would have rejected qwen3-next never has to fire in setup
// because nothing incompatible is in the catalog to begin with.
func TestCatalog_AllArchesSupported(t *testing.T) {
	cat, err := loadCatalog()
	if err != nil {
		t.Fatalf("loadCatalog: %v", err)
	}
	for id, m := range cat.Models {
		if !llamacompat.Supported(m.Architecture) {
			t.Errorf("model %q has arch %q which is NOT in the gate's supported set", id, m.Architecture)
		}
	}
}

// TestCatalog_AgentTiersSupportTools guards the tiers that drive the agent
// loop. everyday and most_capable serve tool-calling turns, so their models
// must advertise tool support; a non-tool model there silently breaks the
// agent. fast tiers and embedding are exempt.
func TestCatalog_AgentTiersSupportTools(t *testing.T) {
	cat, err := loadCatalog()
	if err != nil {
		t.Fatalf("loadCatalog: %v", err)
	}
	for name, prof := range cat.Profiles {
		for _, tier := range []string{"everyday", "most_capable"} {
			m := cat.Models[prof[tier]]
			if !m.SupportsTools {
				t.Errorf("profile %q tier %q model %q does not support tools", name, tier, m.ID)
			}
		}
	}
}

// TestCatalog_EmbeddingTierEmbeds ensures each profile's embedding slot is an
// actual encoder model, not a chat model dropped in by mistake.
func TestCatalog_EmbeddingTierEmbeds(t *testing.T) {
	cat, err := loadCatalog()
	if err != nil {
		t.Fatalf("loadCatalog: %v", err)
	}
	for name, prof := range cat.Profiles {
		m := cat.Models[prof["embedding"]]
		if !m.SupportsEmbed {
			t.Errorf("profile %q embedding model %q is not an embedder", name, m.ID)
		}
	}
}

// TestProfileForRAM checks the "largest profile at or below the machine's RAM"
// selection, including the below-smallest floor and the above-largest cap.
func TestProfileForRAM(t *testing.T) {
	cat, err := loadCatalog()
	if err != nil {
		t.Fatalf("loadCatalog: %v", err)
	}
	const gib = uint64(1024 * 1024 * 1024)
	cases := []struct {
		ramGiB uint64
		want   int
	}{
		{16, 24},   // below smallest -> smallest
		{24, 24},   // exact
		{32, 24},   // between -> lower
		{48, 48},   // exact
		{64, 48},   // between -> lower
		{96, 96},   // exact
		{128, 128}, // exact
		{256, 128}, // above largest -> largest
	}
	for _, tc := range cases {
		_, chosen := cat.ProfileForRAM(tc.ramGiB * gib)
		if chosen != tc.want {
			t.Errorf("ProfileForRAM(%d GiB) chose %d, want %d", tc.ramGiB, chosen, tc.want)
		}
	}
}

// TestCuratedModel_DownloadURLs verifies the HF resolve-URL construction,
// including the multi-shard case (GLM-4.5-Air) where every shard must get its
// own URL in order.
func TestCuratedModel_DownloadURLs(t *testing.T) {
	cat, err := loadCatalog()
	if err != nil {
		t.Fatalf("loadCatalog: %v", err)
	}
	glm, ok := cat.Models["glm-4.5-air-q4_k_m"]
	if !ok {
		t.Fatal("expected glm-4.5-air-q4_k_m in catalog")
	}
	urls := glm.DownloadURLs()
	if len(urls) != 2 {
		t.Fatalf("GLM should yield 2 shard URLs, got %d", len(urls))
	}
	want0 := "https://huggingface.co/unsloth/GLM-4.5-Air-GGUF/resolve/main/Q4_K_M/GLM-4.5-Air-Q4_K_M-00001-of-00002.gguf"
	if urls[0] != want0 {
		t.Errorf("shard 0 URL = %q, want %q", urls[0], want0)
	}
}
