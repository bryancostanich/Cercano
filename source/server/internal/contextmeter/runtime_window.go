package contextmeter

import (
	"strings"

	"cercano/source/server/internal/localruntime/llamaserver"
	"cercano/source/server/internal/sysram"
	"cercano/source/server/pkg/config"
)

// MeterWindow reports the context window the meter should measure against for
// model under cfg's locus mode, and whether that number is authoritative.
//
// The two locus routes need different sources of truth:
//
//   - cloud routes: the window is a property of the remote model, so the
//     published per-family table (ModelWindowFor) is authoritative.
//   - local routes: the window is whatever we launch the runtime with, so the
//     configured context size wins. A local model's published window is
//     irrelevant when llama-server is started with a smaller --ctx-size, and
//     guessing the family default over-reports headroom by ~8x.
//
// This is why the local branch does NOT consult ModelWindowFor: a local model
// that happens to match a known family (e.g. a qwen build) would otherwise
// report its published 128K while the runtime actually serves the configured
// size. Config is the only thing that reflects the process we really spawn.
//
// The local size is read from cfg at call time, so changing llama_server's
// context_size (or mistralrs's max_seq_len) moves the meter with no code change.
func MeterWindow(cfg config.Config, model string) ModelWindow {
	switch cfg.LocusMode {
	case "open_primary", "open_only":
		if n := LocalRuntimeWindow(cfg, model); n > 0 {
			return ModelWindow{Tokens: n, Known: true}
		}
		// Config yielded nothing usable (e.g. no runtime configured); fall
		// through to the published table rather than reporting a zero window,
		// which would render as a divide-by-zero meter.
		return ModelWindowFor(model)
	default:
		return ModelWindowFor(model)
	}
}

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
