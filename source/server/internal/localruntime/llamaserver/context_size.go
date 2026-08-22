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

// stripFlag removes every occurrence of a flag that takes one following value.
func stripFlag(args []string, flag string) []string {
	out := args[:0]
	for i := 0; i < len(args); i++ {
		if args[i] == flag {
			if i+1 < len(args) {
				i++
			}
			continue
		}
		out = append(out, args[i])
	}
	return out
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

// ContextSizeInput is the complete precedence set for resolving the window a
// llama-server process should serve.
type ContextSizeInput struct {
	ConfigContextSize  int
	ConfigExplicit     bool
	ProfileContextSize int
	ModelExtraArgs     []string
	DefaultContextSize int
}

// EffectiveContextSize reports the context window llama-server will actually
// serve for a model, applying the same precedence as launch args:
//
//	explicit user config llama_server.context_size
//	> profile ctx_size
//	> model extra_args --ctx-size (legacy/backward compatibility)
//	> default context_size
//
// This exists so launch args, the memory guard, tool-loop preflight, and
// sub-agent preflight all budget against the same window. The earlier
// model-extra-args-only rule fixed a false 16k ceiling, but it left ctx-size as
// model-level policy; RAM profiles now own that tuning.
func EffectiveContextSize(in ContextSizeInput) int {
	if in.ConfigExplicit && in.ConfigContextSize > 0 {
		return in.ConfigContextSize
	}
	if in.ProfileContextSize > 0 {
		return in.ProfileContextSize
	}
	if n := ctxSizeFromArgs(in.ModelExtraArgs); n > 0 {
		return n
	}
	if in.DefaultContextSize > 0 {
		return in.DefaultContextSize
	}
	return in.ConfigContextSize
}

// ModelContextOverride returns the profile/model context override for modelID,
// or 0 when none applies. It exists for call sites that only know a bare catalog
// model ID and cannot see a ModelRecord. totalBytes selects the RAM profile.
func ModelContextOverride(modelID string, totalBytes uint64) int {
	cat, err := loadCatalog()
	if err != nil {
		return 0
	}
	profile, _ := cat.ProfileForRAMEntries(totalBytes)
	for _, entry := range profile {
		if entry.Model == modelID && entry.ContextSize > 0 {
			return entry.ContextSize
		}
	}
	if m, ok := cat.Models[modelID]; ok {
		return ctxSizeFromArgs(m.ExtraArgs)
	}
	return 0
}

func BareCatalogID(model string) string {
	for i := len(model) - 1; i >= 0; i-- {
		if model[i] == ':' {
			return model[i+1:]
		}
	}
	return model
}
