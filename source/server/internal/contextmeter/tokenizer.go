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

// ModelWindow reports the context-window size selected for a model plus whether
// the value came from known model metadata. Unknown models still get an
// operational default so callers can keep working, but UIs should label that
// denominator as estimated/defaulted rather than known provider capacity.
type ModelWindow struct {
	Tokens int
	Known  bool
}

// ModelWindowFor returns the conventional max context-window size for the given
// model name and whether the value is known. Names are matched
// case-insensitively against substrings, so "qwen3-coder:latest" matches
// "qwen3-coder". Unknown models default to 128 000 with Known=false.
func ModelWindowFor(model string) ModelWindow {
	if max, ok := KnownModelMax(model); ok {
		return ModelWindow{Tokens: max, Known: true}
	}
	return ModelWindow{Tokens: 128_000, Known: false}
}

// modelMax returns the conventional max context-window size for the given
// model name. Used as the denominator of the % display in the CLI.
//
// Names are matched case-insensitively against substrings, so
// "qwen3-coder:latest" matches "qwen3-coder". Default is 128 000.
func ModelMax(model string) int { return ModelWindowFor(model).Tokens }

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

// Memoization bounds. Turn content is immutable once written, so the same
// strings are re-counted many times: once per Assemble pass, and again on
// every request for the life of the conversation. Short strings tokenize
// fast enough that caching them wastes entries, so only sizeable blocks are
// memoized.
const (
	minMemoLen     = 256
	maxMemoEntries = 1 << 16
)

// memoEntry stores a cached count alongside the source length. Length is
// checked on lookup so a hash collision must also match the exact byte
// length before a stale count can be returned.
type memoEntry struct {
	length int
	count  int
}

// memoTokenizer wraps a Tokenizer with a bounded, concurrency-safe cache
// keyed on a 64-bit FNV-1a hash of the input.
//
// Keying on the hash rather than the string matters: a map with string keys
// would pin every counted block in memory for the process lifetime (tens of
// megabytes for a large conversation), because the key holds a reference to
// the original bytes. The hash keeps the cache flat regardless of content
// size.
//
// Collision risk is accepted deliberately and is negligible in practice: a
// 64-bit hash plus an exact length check, over a cache capped at 65 536
// entries, puts the odds of a wrong count far below the error already
// inherent in using cl100k_base as a proxy for non-OpenAI tokenizers. The
// counts drive budget decisions, not correctness of message content.
type memoTokenizer struct {
	inner Tokenizer

	mu    sync.RWMutex
	cache map[uint64]memoEntry
}

// Memoizing wraps tok so repeated Count calls on identical strings are served
// from cache. Returns tok unchanged if it is nil or already memoizing.
func Memoizing(tok Tokenizer) Tokenizer {
	if tok == nil {
		return nil
	}
	if _, ok := tok.(*memoTokenizer); ok {
		return tok
	}
	return &memoTokenizer{inner: tok, cache: make(map[uint64]memoEntry)}
}

func (t *memoTokenizer) Count(s string) int {
	if len(s) < minMemoLen {
		return t.inner.Count(s)
	}
	key := hashString(s)

	t.mu.RLock()
	e, ok := t.cache[key]
	t.mu.RUnlock()
	if ok && e.length == len(s) {
		return e.count
	}

	n := t.inner.Count(s)

	t.mu.Lock()
	// Bounded by wholesale reset rather than LRU eviction: the access pattern
	// is dominated by one conversation's working set, so a rare full clear is
	// cheaper than per-entry bookkeeping on every lookup.
	if len(t.cache) >= maxMemoEntries {
		t.cache = make(map[uint64]memoEntry)
	}
	t.cache[key] = memoEntry{length: len(s), count: n}
	t.mu.Unlock()

	return n
}

// hashString is FNV-1a over the raw bytes, computed without allocating.
func hashString(s string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
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
			// The fallback is len/4 arithmetic — already cheaper than a map
			// lookup, so memoizing it would only add overhead.
			defaultTok = fallbackTokenizer{}
			return
		}
		// Memoized: the shared default is called repeatedly on identical,
		// immutable turn content during request assembly, where re-encoding
		// dominates request latency on large conversations.
		defaultTok = Memoizing(&tiktokenTokenizer{enc: enc})
	})
	return defaultTok
}
