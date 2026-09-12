package modelwindow

import (
	"cercano/source/server/internal/contextmeter"

	"cercano/source/server/pkg/config"
)

// MeterWindow preserves cloud/other-runtime policy. llama-server requires a live
// runtime resolver; config alone returns unknown, never a family-name guess.
func MeterWindow(cfg config.Config, model string) contextmeter.ModelWindow {
	switch cfg.LocusMode {
	case "open_primary", "open_only":
		if cfg.OpenRuntime == "llama_server" {
			return contextmeter.ModelWindow{}
		}
		if n := LocalRuntimeWindow(cfg, model); n > 0 {
			return contextmeter.ModelWindow{Tokens: n, Known: true}
		}
		// Config yielded nothing usable (e.g. no runtime configured); fall
		// through to the published table rather than reporting a zero window,
		// which would render as a divide-by-zero meter.
		return contextmeter.ModelWindowFor(model)
	default:
		return contextmeter.ModelWindowFor(model)
	}
}

// LocalRuntimeWindow retains static policy for other runtimes. Managed
// llama-server capacity must be obtained from its live runtime provider.
func LocalRuntimeWindow(cfg config.Config, model string) int {
	switch cfg.OpenRuntime {
	case "mistralrs":
		return cfg.MistralRS.MaxSeqLen
	case "llama_server":
		// Configuration/catalog policy is not a serving-capacity observation.
		return 0
	default:
		// Preserve the legacy policy of other backends. This branch never
		// supplies capacity for the managed llama-server case above.
		if cfg.LlamaServer.ContextOverride() > 0 {
			return cfg.LlamaServer.ContextOverride()
		}
		return cfg.MistralRS.MaxSeqLen
	}
}
