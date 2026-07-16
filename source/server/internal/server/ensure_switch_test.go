package server

import (
	"reflect"
	"testing"

	"cercano/source/server/pkg/config"
)

// TestRuntimeWantedModels pins the config→policy layer that feeds the
// engine-agnostic EnsureModelsPresent: each runtime contributes its configured
// default (the seed of its "tier set"), and ollama — which manages its own
// model presence — wants nothing from this path. This is the ONE place a
// runtime name is inspected on the download-on-switch path; everything below
// (resolve + fetch) is uniform.
func TestRuntimeWantedModels(t *testing.T) {
	cfg := config.Config{
		LlamaServer: config.LlamaServerConfig{DefaultModel: "/models/qwen3.gguf"},
		MistralRS:   config.MistralRSConfig{DefaultModel: "mistralrs:catalog:qwen3-1.7b"},
	}

	cases := []struct {
		runtime string
		want    []string
	}{
		{"llama_server", []string{"/models/qwen3.gguf"}},
		{"mistralrs", []string{"mistralrs:catalog:qwen3-1.7b"}},
		{"ollama", nil},
		{"unknown", nil},
	}
	for _, c := range cases {
		got := runtimeWantedModels(cfg, c.runtime)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("runtimeWantedModels(%q) = %v, want %v", c.runtime, got, c.want)
		}
	}
}

// TestRuntimeWantedModels_UnsetDefaults proves a runtime with no configured
// default wants nothing (no spurious empty-ref fetch attempt).
func TestRuntimeWantedModels_UnsetDefaults(t *testing.T) {
	cfg := config.Config{}
	if got := runtimeWantedModels(cfg, "llama_server"); got != nil {
		t.Errorf("llama_server with no default = %v, want nil", got)
	}
	if got := runtimeWantedModels(cfg, "mistralrs"); got != nil {
		t.Errorf("mistralrs with no default = %v, want nil", got)
	}
}

func TestRebindOpenTiersForRuntime(t *testing.T) {
	cfg := config.Config{
		Models: config.ModelsConfig{Tiers: config.ModelTiers{
			MostCapable:   config.ModelTier{Open: "glm-gguf"},
			Everyday:      config.ModelTier{Open: "qwen-gguf"},
			FastLight:     config.ModelTier{Open: "phi-gguf"},
			FastLightText: config.ModelTier{Open: "phi-text-gguf"},
		}},
		MistralRS: config.MistralRSConfig{DefaultModel: "qwen3-30b-a3b-instruct-2507"},
	}

	if got := rebindOpenTiersForRuntime(&cfg, "mistralrs"); got != "qwen3-30b-a3b-instruct-2507" {
		t.Fatalf("rebindOpenTiersForRuntime = %q", got)
	}
	if cfg.Models.Tiers.Everyday.Open != "qwen3-30b-a3b-instruct-2507" ||
		cfg.Models.Tiers.FastLight.Open != "qwen3-30b-a3b-instruct-2507" ||
		cfg.Models.Tiers.FastLightText.Open != "qwen3-30b-a3b-instruct-2507" {
		t.Fatalf("runtime switch did not rebind dispatch/chat tiers: %#v", cfg.Models.Tiers)
	}
	if cfg.Models.Tiers.MostCapable.Open != "glm-gguf" {
		t.Fatalf("most_capable should not be rewritten by a latency-tier runtime switch: %q", cfg.Models.Tiers.MostCapable.Open)
	}
}

func TestRebindOpenTiersForRuntime_NoDefaultIsNoop(t *testing.T) {
	cfg := config.Config{
		Models: config.ModelsConfig{Tiers: config.ModelTiers{
			Everyday: config.ModelTier{Open: "keep-me"},
		}},
	}
	if got := rebindOpenTiersForRuntime(&cfg, "mistralrs"); got != "" {
		t.Fatalf("rebindOpenTiersForRuntime = %q, want empty", got)
	}
	if cfg.Models.Tiers.Everyday.Open != "keep-me" {
		t.Fatalf("unexpected tier rewrite without runtime default: %q", cfg.Models.Tiers.Everyday.Open)
	}
}
