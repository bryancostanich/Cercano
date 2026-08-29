package runner

import (
	"testing"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/pkg/config"
)

// The bug this guards: profile context, not the default config value, is the
// effective local context window for curated catalog models. On the 128 GB test
// host, GLM should resolve to 128K, not the default/config fallback.
func TestLocalContextWindow_UsesProfileContextNotConfigDefault(t *testing.T) {
	cfg := config.Config{OpenRuntime: "llama_server"}
	cfg.LlamaServer.ContextSize = 16384

	got := contextmeter.LocalRuntimeWindow(cfg, "llama_server:catalog:glm-4.5-air-q4_k_m")
	if got != 131072 {
		t.Fatalf("LocalRuntimeWindow = %d, want 131072 (128GB profile override)", got)
	}
	// The 32.5K-token sub-agent request from LUNIE - GLOBE INTEGRATION must fit.
	if 32506 > got {
		t.Fatalf("32506-token request still exceeds resolved window %d", got)
	}
}

func TestLocalContextWindow_FallsBackToConfigWithoutOverride(t *testing.T) {
	cfg := config.Config{OpenRuntime: "llama_server"}
	cfg.LlamaServer.ContextSize = 16384

	for _, model := range []string{"", "llama_server:catalog:no-such-model"} {
		if got := contextmeter.LocalRuntimeWindow(cfg, model); got != 16384 {
			t.Fatalf("LocalRuntimeWindow(%q) = %d, want config 16384", model, got)
		}
	}
}

func TestLocalContextWindow_BareModelIDAlsoResolves(t *testing.T) {
	cfg := config.Config{OpenRuntime: "llama_server"}
	cfg.LlamaServer.ContextSize = 16384

	if got := contextmeter.LocalRuntimeWindow(cfg, "glm-4.5-air-q4_k_m"); got != 131072 {
		t.Fatalf("LocalRuntimeWindow(bare id) = %d, want 131072", got)
	}
}

func TestLocalContextWindow_ExplicitConfigOverridesProfile(t *testing.T) {
	cfg := config.Config{OpenRuntime: "llama_server"}
	cfg.LlamaServer.ContextSize = 65536
	cfg.LlamaServer.ContextSizeSet = true

	if got := contextmeter.LocalRuntimeWindow(cfg, "glm-4.5-air-q4_k_m"); got != 65536 {
		t.Fatalf("explicit LocalRuntimeWindow = %d, want 65536", got)
	}
}

func TestLocalContextWindow_MistralRSUnaffected(t *testing.T) {
	cfg := config.Config{OpenRuntime: "mistralrs"}
	cfg.MistralRS.MaxSeqLen = 8192
	cfg.LlamaServer.ContextSize = 16384

	if got := contextmeter.LocalRuntimeWindow(cfg, "glm-4.5-air-q4_k_m"); got != 8192 {
		t.Fatalf("mistralrs window = %d, want MaxSeqLen 8192", got)
	}
}
