package runner

import (
	"testing"

	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/modelwindow"
	"cercano/source/server/pkg/config"
)

// The bug this guards: profile context, not the default config value, is the
// effective local context window for curated catalog models. On the 128 GB test
// host, GLM should resolve to 128K, not the default/config fallback.
func TestLocalContextWindow_UsesProfileContextNotConfigDefault(t *testing.T) {
	cfg := config.Config{OpenRuntime: "llama_server"}
	cfg.LlamaServer.ContextSize = 16384

	got := modelwindow.LocalRuntimeWindow(cfg, "llama_server:catalog:glm-4.5-air-q4_k_m")
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
		if got := modelwindow.LocalRuntimeWindow(cfg, model); got != 16384 {
			t.Fatalf("LocalRuntimeWindow(%q) = %d, want config 16384", model, got)
		}
	}
}

func TestLocalContextWindow_BareModelIDAlsoResolves(t *testing.T) {
	cfg := config.Config{OpenRuntime: "llama_server"}
	cfg.LlamaServer.ContextSize = 16384

	if got := modelwindow.LocalRuntimeWindow(cfg, "glm-4.5-air-q4_k_m"); got != 131072 {
		t.Fatalf("LocalRuntimeWindow(bare id) = %d, want 131072", got)
	}
}

func TestLocalContextWindow_ExplicitConfigOverridesProfile(t *testing.T) {
	cfg := config.Config{OpenRuntime: "llama_server"}
	cfg.LlamaServer.ContextSize = 65536
	cfg.LlamaServer.ContextSizeSet = true

	if got := modelwindow.LocalRuntimeWindow(cfg, "glm-4.5-air-q4_k_m"); got != 65536 {
		t.Fatalf("explicit LocalRuntimeWindow = %d, want 65536", got)
	}
}

// This service-seam regression demonstrates that editing config changes the
// budget without any runtime operation. It is not a live-runtime integration
// test: once runtime-owned capacity is wired, inject a confirmed instance here
// and add separate lifecycle/confirmation integration coverage.
func TestLocalContextWindow_ConfigEditDoesNotChangeServingCapacity(t *testing.T) {
	cfg := config.Config{OpenRuntime: "llama_server"}
	cfg.LlamaServer.ContextSize = 65536
	cfg.LlamaServer.ContextSizeSet = true
	svc := cfgsvc.New("", cfg, nil)
	core := &Core{d: Deps{Config: svc}}
	const model = "glm-4.5-air-q4_k_m"
	before, known := core.knownContextWindowFor(false, model)
	if !known || before != 65536 {
		t.Fatalf("initial capacity = (%d, %v), want (65536, true)", before, known)
	}
	cfg.LlamaServer.ContextSize = 8192
	svc.Set(cfg)
	after, known := core.knownContextWindowFor(false, model)
	if !known || after != before {
		t.Fatalf("config-only edit changed serving budget from %d to (%d, %v) without a runtime restart", before, after, known)
	}
}

func TestLocalContextWindow_MistralRSUnaffected(t *testing.T) {
	cfg := config.Config{OpenRuntime: "mistralrs"}
	cfg.MistralRS.MaxSeqLen = 8192
	cfg.LlamaServer.ContextSize = 16384

	if got := modelwindow.LocalRuntimeWindow(cfg, "glm-4.5-air-q4_k_m"); got != 8192 {
		t.Fatalf("mistralrs window = %d, want MaxSeqLen 8192", got)
	}
}
