package llamaserver

import "strconv"

// hasFlag reports whether args contains the exact flag token. Launch args are
// always emitted as separate "--flag", "value" elements, so an exact match on
// the flag token is sufficient.
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// ctxSizeFromArgs returns the value of the LAST --ctx-size in args, matching
// llama-server's own last-flag-wins parsing. Returns 0 when absent or unparsable.
func ctxSizeFromArgs(args []string) int {
	out := 0
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "--ctx-size" {
			continue
		}
		if n, err := strconv.Atoi(args[i+1]); err == nil && n > 0 {
			out = n
		}
	}
	return out
}

// EffectiveContextSize reports the context window llama-server will actually
// serve for a model, applying the same precedence as the launch command:
// a per-model --ctx-size from the catalog's ExtraArgs wins over the global
// config value, because per-model flags are appended last and llama-server
// honors the last occurrence.
//
// This exists so the agent's context budget is computed from the window the
// server is really running, not from config alone. Budgeting against a smaller
// config number causes false preflight context_overflow rejections for requests
// the model would have accepted; budgeting against a larger one lets genuinely
// oversized requests reach the server and fail there.
//
// modelExtraArgs is the catalog entry's ExtraArgs for the running model.
// Returns configContextSize when the model pins nothing.
func EffectiveContextSize(configContextSize int, modelExtraArgs []string) int {
	if n := ctxSizeFromArgs(modelExtraArgs); n > 0 {
		return n
	}
	return configContextSize
}

// ModelContextOverride returns the per-model --ctx-size pinned in the catalog
// for modelID, or 0 when the model pins none (or the ID is unknown). Callers
// outside this package use it to resolve the effective window without needing
// the catalog's internal shape.
func ModelContextOverride(modelID string) int {
	cat, err := loadCatalog()
	if err != nil {
		return 0
	}
	m, ok := cat.Models[modelID]
	if !ok {
		return 0
	}
	return ctxSizeFromArgs(m.ExtraArgs)
}
