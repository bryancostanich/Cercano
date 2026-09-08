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

// localTailContextFraction is the share of a local/open context window used for
// automatic history tailing. It is intentionally lower than the hard preflight
// guard so the reduced prompt has room for provider-specific overhead and the
// model's answer.
const localTailContextFraction = 0.8

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

func reduceHistoryToContextTail(system string, history []llm.Message, userInput string, images int, window int) ([]llm.Message, bool) {
	if window <= 0 || len(history) == 0 {
		return history, false
	}
	budget := int(float64(window) * localTailContextFraction)
	fixed := estimateTokens(system) + estimateTokens(userInput) + images*perImageTokenEstimate
	remaining := budget - fixed
	if remaining <= 0 {
		return nil, len(history) > 0
	}

	kept := make([]llm.Message, 0, len(history))
	used := 0
	for i := len(history) - 1; i >= 0; i-- {
		cost := estimateMessageTokens(history[i])
		if used+cost > remaining {
			break
		}
		used += cost
		kept = append(kept, history[i])
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return kept, len(kept) != len(history)
}

// preflightContextCheck estimates the total prompt size (system prompt +
// prior history + this turn's user input + any inline images) and returns a
// classified llm.ErrContextOverflow when that estimate exceeds window scaled by
// preflightSafetyFraction. window <= 0 disables the check (the caller could not
// resolve a window for this model, so there is nothing to check against).
//
// The returned error is the same class the providers mint on a real overflow,
// so callers and the resilience engine treat a pre-flight refusal and a
// provider-reported overflow identically — except this one costs no round-trip
// and, for a local model, no warm-up.
func preflightContextCheck(system string, history []llm.Message, userInput string, images int, window int) error {
	if window <= 0 {
		return nil
	}
	messages := append([]llm.Message{}, history...)
	messages = append(messages, llm.Message{Role: llm.RoleUser, Blocks: buildUserBlocks(userInput, make([]InlineImage, images))})
	budget := EstimateRequestBudget(RequestBudgetInput{System: system, Messages: messages, ContextWindow: window})
	if budget.Fits {
		return nil
	}
	return budget.OverflowError()
}
