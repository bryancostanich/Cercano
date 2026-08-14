package agent

import (
	"fmt"

	"cercano/source/server/internal/llm"
)

// preflightSafetyFraction is the share of a model's context window the
// estimated prompt may occupy before the pre-flight check refuses to start the
// loop. The estimate (estimateTokens) is deliberately rough — roughly four
// characters per token — so the guard flags at a margin below the true window
// rather than at 100%. This trades a few false negatives (a prompt that
// squeaks in just over the fraction but under the real window) for near-zero
// false positives that would block legitimate work. When the estimate is
// optimistic and the real request still overflows, the provider's own
// ErrContextOverflow (see llm/context_overflow.go) remains the backstop.
const preflightSafetyFraction = 0.9

// localTailContextFraction is the share of a local/open context window used for
// automatic history tailing. It is intentionally lower than the hard preflight
// guard so the reduced prompt has room for provider-specific overhead and the
// model's answer.
const localTailContextFraction = 0.8

// estimateTokens returns a rough token count for s using the standard
// ~4-characters-per-token heuristic. It is intentionally cheap and
// provider-agnostic: no tokenizer, no network round-trip, no per-model vocab.
// It exists to power a fail-fast guardrail, not to meter billing, so an error
// of ±10-20% is acceptable — the guard applies a safety fraction on top.
func estimateTokens(s string) int {
	// Round up so a short non-empty string never estimates to zero tokens.
	return (len(s) + 3) / 4
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
	used := estimateTokens(system) + estimateTokens(userInput)
	for _, m := range history {
		used += estimateMessageTokens(m)
	}
	used += images * perImageTokenEstimate

	budget := int(float64(window) * preflightSafetyFraction)
	if used <= budget {
		return nil
	}
	return &llm.Error{
		Class:    llm.ErrContextOverflow,
		Provider: "preflight",
		Used:     used,
		Limit:    window,
		Err: fmt.Errorf(
			"sub-agent prompt is ~%d tokens but this tier's model holds %d; trim the task or its context, or raise the model's context window",
			used, window),
	}
}
