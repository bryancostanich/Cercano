package llamaserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// TestCatalog_PlainChatTiersAreChatCapable guards the newer plain-chat gate:
// every required tier except embedding is a plain-chat tier, so its model must
// produce visible chat content. GLM-4.5-Air loads and does tool calls but
// returns empty plain-chat output on the pinned build, so it must not appear in
// any chat tier — the loader enforces this, and this test asserts the shipped
// data satisfies it.
func TestCatalog_PlainChatTiersAreChatCapable(t *testing.T) {
	cat, err := loadCatalog()
	if err != nil {
		t.Fatalf("loadCatalog: %v", err)
	}
	for name, prof := range cat.Profiles {
		for tier, id := range prof {
			if tier == "embedding" {
				continue
			}
			if !cat.Models[id].PlainChatSupported() {
				t.Errorf("profile %q chat tier %q uses non-plain-chat model %q", name, tier, id)
			}
		}
	}
}

// TestCatalog_GLMPlainChatRecovered pins the post-recovery facts: once the
// OpenAI adapter recovers GLM's answer from reasoning_content (see effort
// glm-reasoning-content-recovery), GLM is plain-chat capable and tool-capable,
// and it MUST carry the "--jinja" launch flag — without it llama-server
// compute-fails at decode, so a GLM entry lacking --jinja would route a broken
// model to any tier that selects it.
func TestCatalog_GLMPlainChatRecovered(t *testing.T) {
	cat, err := loadCatalog()
	if err != nil {
		t.Fatalf("loadCatalog: %v", err)
	}
	glm, ok := cat.Models["glm-4.5-air-q4_k_m"]
	if !ok {
		t.Fatal("expected glm-4.5-air-q4_k_m in catalog")
	}
	if !glm.PlainChatSupported() {
		t.Error("GLM must be plain_chat_ok:true after reasoning-content recovery")
	}
	if !glm.SupportsTools {
		t.Error("GLM should advertise tool support")
	}
	hasJinja := false
	for _, a := range glm.ExtraArgs {
		if a == "--jinja" {
			hasJinja = true
		}
	}
	if !hasJinja {
		t.Errorf("GLM must launch with --jinja (extra_args=%v); without it llama-server compute-fails", glm.ExtraArgs)
	}
}

// TestLoadCatalog_RejectsNonPlainChatInChatTier asserts the loader itself
// fails a catalog that places a non-plain-chat model in a chat tier, so a bad
// authoring edit is caught at build time rather than recommending an
// empty-output model to a user.
func TestLoadCatalog_RejectsNonPlainChatInChatTier(t *testing.T) {
	no := false
	cat := CuratedCatalog{
		Models: map[string]CuratedModel{
			"chatty":   {ID: "chatty", SupportsTools: true},
			"emptybot": {ID: "emptybot", SupportsTools: true, PlainChatOK: &no},
			"embedder": {ID: "embedder", SupportsEmbed: true},
		},
		Profiles: map[string]map[string]string{
			"48": {
				"most_capable":    "emptybot",
				"everyday":        "chatty",
				"fast_light":      "chatty",
				"fast_light_text": "chatty",
				"embedding":       "embedder",
			},
		},
	}
	if err := validateCatalog(cat); err == nil {
		t.Fatal("expected validation error for non-plain-chat model in most_capable tier")
	} else if !strings.Contains(err.Error(), "plain-chat") {
		t.Fatalf("error should mention plain-chat, got: %v", err)
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

func TestRecommendedOpenModels(t *testing.T) {
	cat, err := loadCatalog()
	if err != nil {
		t.Fatalf("loadCatalog: %v", err)
	}
	const ram = 128 * 1024 * 1024 * 1024 // 128 GB
	profile, _ := cat.ProfileForRAM(ram)

	got := RecommendedOpenModels(ram)
	if len(got) != len(profile) {
		t.Fatalf("tier count: want %d, got %d", len(profile), len(got))
	}
	// Every recommendation must be the stable inventory id
	// ("llama_server:catalog:<bareID>") that catalogModels() surfaces and the
	// finish-time download path matches on — the bare ProfileForRAM id prefixed.
	for tier, bareID := range profile {
		want := runtimeName + ":catalog:" + bareID
		if got[tier] != want {
			t.Errorf("tier %q: want %q, got %q", tier, want, got[tier])
		}
		if !strings.HasPrefix(got[tier], "llama_server:catalog:") {
			t.Errorf("tier %q: id %q lacks the inventory prefix", tier, got[tier])
		}
	}
}

// TestCuratedModel_VisionFieldsRoundTrip confirms the new mmproj/vision catalog
// fields parse from JSON, so an authored vision entry carries its projector.
func TestCuratedModel_VisionFieldsRoundTrip(t *testing.T) {
	const raw = `{
		"id": "qwen2.5-vl-7b",
		"files": ["model.gguf", "mmproj-f16.gguf"],
		"supports_vision": true,
		"mmproj_file": "mmproj-f16.gguf"
	}`
	var m CuratedModel
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !m.SupportsVision {
		t.Error("SupportsVision did not parse as true")
	}
	if m.MmprojFile != "mmproj-f16.gguf" {
		t.Errorf("MmprojFile = %q, want mmproj-f16.gguf", m.MmprojFile)
	}
	// A non-vision entry leaves both zero-valued.
	var plain CuratedModel
	if err := json.Unmarshal([]byte(`{"id":"x"}`), &plain); err != nil {
		t.Fatalf("unmarshal plain: %v", err)
	}
	if plain.SupportsVision || plain.MmprojFile != "" {
		t.Errorf("non-vision entry got vision fields: %+v", plain)
	}
}

// TestResolveVision covers the gate: vision is on ONLY when the catalog flag is
// set, MmprojFile is named, AND that file physically exists in the model dir.
func TestResolveVision(t *testing.T) {
	dir := t.TempDir()
	projector := "mmproj-f16.gguf"
	if err := os.WriteFile(filepath.Join(dir, projector), []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("vision + file present", func(t *testing.T) {
		path, ok := resolveVision(dir, CuratedModel{SupportsVision: true, MmprojFile: projector})
		if !ok || path != filepath.Join(dir, projector) {
			t.Errorf("got (%q, %v), want the resolved path + true", path, ok)
		}
	})
	t.Run("vision flag but file missing", func(t *testing.T) {
		path, ok := resolveVision(dir, CuratedModel{SupportsVision: true, MmprojFile: "absent.gguf"})
		if ok || path != "" {
			t.Errorf("missing projector should downgrade to text-only, got (%q, %v)", path, ok)
		}
	})
	t.Run("file present but flag off", func(t *testing.T) {
		if _, ok := resolveVision(dir, CuratedModel{MmprojFile: projector}); ok {
			t.Error("no SupportsVision flag should mean no vision")
		}
	})
	t.Run("non-vision model", func(t *testing.T) {
		if path, ok := resolveVision(dir, CuratedModel{}); ok || path != "" {
			t.Errorf("plain model should be text-only, got (%q, %v)", path, ok)
		}
	})
}
