package agent

import (
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

// preflightSafetyFraction is the share of a model's context window the
// counted prompt may occupy before the pre-flight check refuses to start the
// loop.
//
// This fraction originally compensated for estimator slop: estimateTokens was
// char/4 arithmetic, and the margin absorbed its error. It no longer serves
// that purpose — counting is now real cl100k_base tokenization — so the
// fraction is retained as deliberate headroom for costs this guard cannot see:
// provider-side prompt scaffolding, tool-schema serialization, and per-model
// tokenizer differences (we count with cl100k as a proxy for Anthropic and
// Qwen, which have their own vocabularies and will disagree by a few percent).
//
// It is NOT tuned against production data. 0.9 is the inherited value, kept
// because re-tuning wants overflow-rate telemetry we do not yet collect.
// When the count is right but the real request still overflows, the provider's
// own ErrContextOverflow (see llm/context_overflow.go) remains the backstop.
const preflightSafetyFraction = 0.9

// estimateTokens returns the token count for s.
//
// Formerly char/4 arithmetic. That undercounted high-entropy content —
// base64, hashes, minified JSON tool results tokenize near 1.8 chars/token,
// where char/4 reports under half the true size — and undercounting is the
// direction that lets an oversized prompt past the guard and into a provider
// overflow. Now backed by real cl100k_base tokenization, memoized and bounded
// (see contextmeter), so it stays cheap enough for a per-turn guardrail.
func estimateTokens(s string) int {
	return contextmeter.Default().Count(s)
}

// estimateMessageTokens sums the estimated tokens across a message's text-bearing
// blocks. Text and tool-result Content carry the bulk of history bytes; image
// payloads are counted structurally (a flat per-image cost) because their
// base64 length bears no relation to the model's image token cost.
func estimateMessageTokens(m llm.Message) int {
	total := 0
	for _, b := range m.Blocks {
		total += estimateTokens(b.Text)
		total += estimateTokens(b.Content)
		if b.Type == llm.BlockImage {
			total += perImageTokenEstimate
		}
	}
	return total
}

// perImageTokenEstimate is a flat, deliberately conservative per-image cost.
// Vision models bill images by tiled resolution, not payload bytes, so a fixed
// estimate is closer than len(base64)/4 would be. It only needs to be in the
// right order of magnitude for the guardrail.
const perImageTokenEstimate = 1000

// NOTE: preflightContextCheck and reduceHistoryToContextTail used to live here.
// Both were unreachable — no production caller in either module, only their own
// unit tests — so they were removed rather than left to imply a guard that never
// ran. The live guard is EstimateRequestBudget/TrimMessagesToBudget (see
// budget.go), which the tool loop calls before every provider request with the
// system prompt and tool catalog included; that is strictly more accurate than
// the removed preflight, which passed neither.
