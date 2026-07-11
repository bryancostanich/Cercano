// Package llamacompat answers one question: can the bundled llama.cpp /
// llama-server build load a model of a given architecture?
//
// This is the compatibility gate. A GGUF file's first four bytes being the
// "GGUF" magic only prove it's a GGUF *container*; whether llama-server can
// actually run it is decided by the `general.architecture` metadata field
// inside, because llama.cpp loads only the architectures compiled into the
// build. Ollama's library (and HuggingFace at large) include architectures
// that only Ollama's own engine — or a newer llama.cpp — can run, so
// "it's a GGUF" is not "llama-server can run it." Callers ask Supported()
// before offering or downloading a model.
//
// The set below is a hand-maintained seed keyed to Cercano's pinned
// llama.cpp build. The authoritative source is that build's
// src/llama-arch.cpp `LLM_ARCH_NAMES` map (architecture enum → name string);
// a follow-up generates this set from that file at vendor time so the gate
// can never claim support the binary lacks. Until then, keep this list in
// sync when the pinned build bumps: add newly-supported architectures here,
// and a model whose architecture is absent is gated out (shown, but not
// downloadable into llama-server) rather than failing at load time.
package llamacompat

import "strings"

// supportedArches is the set of GGUF `general.architecture` values the
// pinned llama.cpp build can load. Names are the canonical lowercase
// identifiers llama.cpp writes into the GGUF header (and that HuggingFace
// echoes back as `gguf.architecture`), so no case folding beyond a trim is
// needed on well-formed input — Normalize handles the ragged edges.
//
// Deliberately ABSENT (gated until the pinned build gains them): brand-new
// architectures such as qwen3-next's hybrid linear-attention design and
// freshly-released MoE families. That absence is the point — it's what stops
// a user pulling a model llama-server can't run.
var supportedArches = map[string]struct{}{
	// Llama family and the many models that reuse its architecture.
	"llama":   {},
	"llama4":  {},
	"mistral": {},
	"mixtral": {},
	// Qwen.
	"qwen":     {},
	"qwen2":    {},
	"qwen2moe": {},
	"qwen2vl":  {},
	"qwen3":    {},
	"qwen3moe": {},
	// Gemma.
	"gemma":   {},
	"gemma2":  {},
	"gemma3":  {},
	"gemma3n": {},
	// Phi.
	"phi2":   {},
	"phi3":   {},
	"phimoe": {},
	// DeepSeek (v2/v3 serve under deepseek2).
	"deepseek":  {},
	"deepseek2": {},
	// GLM / ChatGLM.
	"chatglm": {},
	"glm4":    {},
	"glm4moe": {},
	// StarCoder / code models.
	"starcoder":  {},
	"starcoder2": {},
	"codeshell":  {},
	"refact":     {},
	// Cohere.
	"command-r": {},
	"cohere2":   {},
	// IBM Granite.
	"granite":       {},
	"granitemoe":    {},
	"granitehybrid": {},
	// Other established chat/text architectures.
	"falcon":    {},
	"stablelm":  {},
	"internlm2": {},
	"minicpm":   {},
	"minicpm3":  {},
	"olmo":      {},
	"olmo2":     {},
	"olmoe":     {},
	"nemotron":  {},
	"exaone":    {},
	"dbrx":      {},
	"gpt2":      {},
	"gptneox":   {},
	"mpt":       {},
	"baichuan":  {},
	"bloom":     {},
	"orion":     {},
	"smollm3":   {},
	// Embedding (encoder) architectures — the embedding tier depends on these.
	"bert":         {},
	"nomic-bert":   {},
	"jina-bert-v2": {},
}

// Normalize canonicalizes a raw architecture string for lookup: trims
// surrounding whitespace and lowercases. HuggingFace's `gguf.architecture`
// and llama.cpp's header value are already canonical, but callers may pass
// values from looser sources (filenames, user input), so we fold here rather
// than trusting the input.
func Normalize(arch string) string {
	return strings.ToLower(strings.TrimSpace(arch))
}

// Supported reports whether the pinned llama.cpp build can load a model of
// this architecture. An empty or unknown architecture returns false — the
// safe default is "don't claim we can run it."
func Supported(arch string) bool {
	if arch == "" {
		return false
	}
	_, ok := supportedArches[Normalize(arch)]
	return ok
}

// SupportedArches returns a copy of the supported architecture identifiers.
// Intended for diagnostics and for the curated-catalog validity test, which
// asserts every catalog entry's architecture is in this set — not for hot
// paths (allocates).
func SupportedArches() []string {
	out := make([]string, 0, len(supportedArches))
	for arch := range supportedArches {
		out = append(out, arch)
	}
	return out
}
