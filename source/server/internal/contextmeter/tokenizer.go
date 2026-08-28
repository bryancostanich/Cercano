// Package contextmeter implements per-model tokenization and per-conversation
// running token-count accounting for the CLI's live context meter.
//
// Token counts are necessarily approximate for models without a published
// tokenizer (Anthropic, Qwen, Llama via Ollama). We use OpenAI's cl100k_base
// BPE via tiktoken-go as a reasonable proxy — close enough that the user
// sees a meaningful "context budget" indicator without us having to ship
// model-specific tokenizers per release.
package contextmeter

import (
	"strings"
	"sync"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

// Tokenizer counts tokens for a string. Implementations are model-specific;
// the registry below picks an appropriate one given a model name.
type Tokenizer interface {
	// Count returns the number of tokens in s.
	Count(s string) int
}

// modelMax returns the conventional max context-window size for the given
// model name. Used as the denominator of the % display in the CLI.
//
// Names are matched case-insensitively against substrings, so
// "qwen3-coder:latest" matches "qwen3-coder". Default is 128 000.
func ModelMax(model string) int {
	if max, ok := KnownModelMax(model); ok {
		return max
	}
	return 128_000
}

// KnownModelMax returns a context-window size only when the model name matches
// a known family. The boolean lets routing policy distinguish a deliberate
// window from ModelMax's conservative default.
func KnownModelMax(model string) (int, bool) {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "qwen3-coder-next"):
		return 262_144, true // qwen3-coder-next:latest publishes 256K
	case strings.Contains(m, "qwen3-coder"):
		return 131_072, true // 128K
	case strings.Contains(m, "qwen"):
		return 131_072, true
	case strings.Contains(m, "claude"):
		// All current Claude models (Opus/Sonnet/Haiku 4.x, Fable/Mythos 5)
		// publish a 200K conventional window. A generic match beats a name
		// list here: an unlisted new Claude model falling through to the
		// 128K default silently shrinks the meter denominator.
		return 200_000, true
	case strings.Contains(m, "gemini-1.5-pro"):
		return 2_000_000, true
	case strings.Contains(m, "gemini"):
		return 1_000_000, true
	case strings.Contains(m, "llama3.1") || strings.Contains(m, "llama-3.1"):
		return 131_072, true
	case strings.Contains(m, "llama"):
		return 8_192, true
	case strings.Contains(m, "nomic-embed"):
		return 8_192, true
	}
	return 0, false
}

// tiktokenTokenizer wraps a tiktoken encoding. Single-instance per encoding
// name; lazily constructed.
type tiktokenTokenizer struct {
	enc *tiktoken.Tiktoken
}

func (t *tiktokenTokenizer) Count(s string) int {
	if t == nil || t.enc == nil || s == "" {
		return 0
	}
	return len(t.enc.Encode(s, nil, nil))
}

// fallbackTokenizer is a char-count/4 estimator used when tiktoken
// initialisation fails (no network for vocabulary download, etc.). Crude but
// keeps the meter advancing instead of showing 0.
type fallbackTokenizer struct{}

func (fallbackTokenizer) Count(s string) int { return (len(s) + 3) / 4 }

// Default returns a usable Tokenizer for any model. cl100k_base covers
// recent OpenAI and gives a reasonable approximation for Anthropic / Qwen /
// Llama BPE tokenization. Returns the fallback if encoding init fails.
//
// The encoding is cached so repeated calls are cheap.
var (
	defaultOnce sync.Once
	defaultTok  Tokenizer
)

func Default() Tokenizer {
	defaultOnce.Do(func() {
		enc, err := tiktoken.GetEncoding("cl100k_base")
		if err != nil {
			defaultTok = fallbackTokenizer{}
			return
		}
		defaultTok = &tiktokenTokenizer{enc: enc}
	})
	return defaultTok
}
