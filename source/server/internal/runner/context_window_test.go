package runner

import (
	"testing"

	"cercano/source/server/pkg/config"
)

// The bug this guards: config pinned LlamaServer.ContextSize=16384 while the
// GLM catalog entry pinned --ctx-size 32768. Per-model flags are appended last
// and llama-server honors the last occurrence, so the process really served
// 32768 — but the tool loop budgeted against 16384 and rejected requests with
// "preflight context_overflow (22127 tokens used vs 16384 limit)" that the
// server would have accepted.
func TestLocalContextWindow_UsesCatalogOverrideNotConfig(t *testing.T) {
	cfg := config.Config{OpenRuntime: "llama_server"}
	cfg.LlamaServer.ContextSize = 16384

	got := localContextWindow(cfg, "llama_server:catalog:glm-4.5-air-q4_k_m")
	if got != 32768 {
		t.Fatalf("localContextWindow = %d, want 32768 (catalog override); "+
			"budgeting against config alone causes false preflight rejections", got)
	}
	// The 22127-token request from the incident must now fit.
	if 22127 > got {
		t.Fatalf("22127-token request still exceeds resolved window %d", got)
	}
}

func TestLocalContextWindow_FallsBackToConfigWithoutOverride(t *testing.T) {
	cfg := config.Config{OpenRuntime: "llama_server"}
	cfg.LlamaServer.ContextSize = 16384

	for _, model := range []string{"", "llama_server:catalog:no-such-model"} {
		if got := localContextWindow(cfg, model); got != 16384 {
			t.Fatalf("localContextWindow(%q) = %d, want config 16384", model, got)
		}
	}
}

func TestLocalContextWindow_BareModelIDAlsoResolves(t *testing.T) {
	cfg := config.Config{OpenRuntime: "llama_server"}
	cfg.LlamaServer.ContextSize = 16384

	if got := localContextWindow(cfg, "glm-4.5-air-q4_k_m"); got != 32768 {
		t.Fatalf("localContextWindow(bare id) = %d, want 32768", got)
	}
}

func TestLocalContextWindow_MistralRSUnaffected(t *testing.T) {
	cfg := config.Config{OpenRuntime: "mistralrs"}
	cfg.MistralRS.MaxSeqLen = 8192
	cfg.LlamaServer.ContextSize = 16384

	if got := localContextWindow(cfg, "glm-4.5-air-q4_k_m"); got != 8192 {
		t.Fatalf("mistralrs window = %d, want MaxSeqLen 8192", got)
	}
}
