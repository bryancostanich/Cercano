package contextmeter

import (
	"strings"

	"cercano/source/server/internal/localruntime/llamaserver"
	"cercano/source/server/internal/sysram"
	"cercano/source/server/pkg/config"
)

// LocalRuntimeWindow reports the context window the configured local runtime
// will actually serve for model, which is NOT always the configured value: a
// curated catalog model may pin its own --ctx-size in ExtraArgs, and because
// per-model flags are appended last, llama-server honors that one instead of
// the config's fallback.
//
// model is the resolved local model ID; empty falls back to the config value.
func LocalRuntimeWindow(cfg config.Config, model string) int {
	switch cfg.OpenRuntime {
	case "mistralrs":
		return cfg.MistralRS.MaxSeqLen
	case "llama_server":
		return LlamaServerWindow(cfg.LlamaServer.ContextSize, cfg.LlamaServer.ContextSizeSet, model)
	default:
		if cfg.LlamaServer.ContextSize > 0 {
			return LlamaServerWindow(cfg.LlamaServer.ContextSize, cfg.LlamaServer.ContextSizeSet, model)
		}
		return cfg.MistralRS.MaxSeqLen
	}
}

// LlamaServerWindow applies the catalog's per-model --ctx-size override to the
// configured context size. The model ID may carry provider/catalog prefixes in
// routing form (e.g. "llama_server:catalog:glm-4.5-air-q4_k_m"); the catalog is
// keyed by the bare ID, so match on the final segment.
func LlamaServerWindow(configured int, configExplicit bool, model string) int {
	if configExplicit && configured > 0 {
		return configured
	}
	if model == "" {
		return configured
	}
	bare := model
	if i := strings.LastIndex(bare, ":"); i >= 0 {
		bare = bare[i+1:]
	}
	total := sysram.Total()
	if total < 0 {
		total = 0
	}
	if n := llamaserver.ModelContextOverride(bare, uint64(total)); n > 0 {
		return n
	}
	return configured
}
