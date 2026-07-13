// Package mistralrscompat answers one question: can the bundled mistral.rs
// build load a model of a given architecture?
//
// It is the mistral.rs sibling of llamacompat — the same compatibility-gate
// idea, a different runtime. mistral.rs loads a model by matching its
// architecture (config.json `architectures[0]` for safetensors, or the GGUF
// `general.architecture`) against the loaders compiled into the build; an
// architecture with no loader fails at load. Callers ask Supported() before
// offering or downloading a model into mistral.rs.
//
// The set below is seeded from mistral.rs's own loader registry — the
// `NormalLoaderType` enum's `FromStr` match arms in
// mistralrs-core/src/pipeline/loaders/normal_loaders.rs (text models). That
// is the authoritative source: it is the exact set of `model_type` strings the
// engine's AutoNormalLoader dispatches on. A follow-up should generate this
// from the pinned build the way llamacompat plans to generate from
// llama-arch.cpp; until then, keep it in sync when the pinned mistral.rs
// version bumps.
//
// Two honest scope notes:
//
//   - Text (normal) loaders only. mistral.rs's vision/multimodal loaders
//     (gemma3, qwen2-vl, idefics, llava, phi3-v, …) are a separate registry
//     and are intentionally not included yet — the chat/tools tiers are text.
//   - Keys are mistral.rs's `model_type` identifiers, which are NOT always
//     spelled the same as llama.cpp's GGUF `general.architecture` (e.g.
//     mistral.rs `deepseekv2` vs llama.cpp `deepseek2`, mistral.rs `phi3.5moe`
//     vs llama.cpp `phimoe`). The catalog backend is responsible for handing
//     this gate an architecture in mistral.rs's space — for a safetensors repo
//     that is `config.json` `architectures[0]` mapped to its `model_type`; for
//     a GGUF served by mistral.rs the two spaces overlap for most families and
//     a mismatch conservatively gates out (safe: no bad download), pending the
//     config.json arch mapping that the online-safetensors discovery adds.
//
// The headline difference from llamacompat: this set INCLUDES qwen3next (and
// the other new hybrid-MoE families) — the architectures llama.cpp can't yet
// load. That divergence is the whole reason mistral.rs is a second runtime.
// (Whether a given qwen3next build is *stable on Metal* is a separate concern —
// the curated Metal catalog holds it back until the upstream fixes release;
// this gate only answers "does a loader exist.")
package mistralrscompat

import "strings"

// supportedArches is the set of mistral.rs `model_type` identifiers the pinned
// build's text-model loaders accept, taken verbatim from NormalLoaderType's
// FromStr in normal_loaders.rs.
var supportedArches = map[string]struct{}{
	"mistral":          {},
	"gemma":            {},
	"mixtral":          {},
	"llama":            {},
	"phi2":             {},
	"phi3":             {},
	"qwen2":            {},
	"gemma2":           {},
	"starcoder2":       {},
	"phi3.5moe":        {},
	"deepseekv2":       {},
	"deepseekv3":       {},
	"qwen3":            {},
	"glm4":             {},
	"glm4moelite":      {},
	"glm4moe":          {},
	"qwen3moe":         {},
	"smollm3":          {},
	"granitemoehybrid": {},
	"gpt_oss":          {},
	"hunyuanv1dense":   {},
	"hunyuanv1moe":     {},
	"qwen3next":        {}, // the point: llama.cpp can't load this; mistral.rs can.
	"lfm2":             {},
	"lfm2_moe":         {},
}

// Normalize canonicalizes a raw architecture string for lookup: trims
// surrounding whitespace and lowercases. mistral.rs's model_type strings are
// already lowercase; folding here tolerates looser callers.
func Normalize(arch string) string {
	return strings.ToLower(strings.TrimSpace(arch))
}

// Supported reports whether the pinned mistral.rs build can load a model of
// this architecture. An empty or unknown architecture returns false — the
// safe default is "don't claim we can run it."
func Supported(arch string) bool {
	if arch == "" {
		return false
	}
	_, ok := supportedArches[Normalize(arch)]
	return ok
}

// SupportedArches returns a copy of the supported architecture identifiers,
// for diagnostics and validity tests (not hot paths — it allocates).
func SupportedArches() []string {
	out := make([]string, 0, len(supportedArches))
	for arch := range supportedArches {
		out = append(out, arch)
	}
	return out
}
