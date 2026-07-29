package server

import (
	"reflect"
	"testing"

	"cercano/source/server/pkg/config"
)

// TestRuntimeWantedModels pins the config→policy layer that feeds the
// engine-agnostic EnsureModelsPresent: each runtime contributes its configured
// default (the seed of its catalog/default path), and ollama — which manages its
// own model presence — wants nothing from this path. This is the ONE place a
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
