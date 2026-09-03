package compaction

import (
	"fmt"
	"sync"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

const (
	DefaultSummaryOutputReserve = 1024
	summaryBudgetSafetyFraction = 0.95
	perImageTokenEstimate       = 768
)

type BudgetResult struct {
	PromptTokens  int
	OutputReserve int
	Limit         int
	Budget        int
	Fits          bool
}

type DeferralError struct {
	Reason string
	Used   int
	Limit  int
}

func (e *DeferralError) Error() string {
	return fmt.Sprintf("compaction deferred: %s (%d tokens used vs %d limit)", e.Reason, e.Used, e.Limit)
}

func EstimateSummaryBudget(prompt string, outputReserve, contextWindow int) BudgetResult {
	if outputReserve <= 0 {
		outputReserve = DefaultSummaryOutputReserve
	}
	res := BudgetResult{PromptTokens: estimateTokens(prompt), OutputReserve: outputReserve, Limit: contextWindow}
	if contextWindow <= 0 {
		res.Fits = true
		return res
	}
	res.Budget = int(float64(contextWindow) * summaryBudgetSafetyFraction)
	res.Fits = res.PromptTokens+res.OutputReserve <= res.Budget
	return res
}

// budgetTokenizer is the tokenizer used for all summary-budget math. It is
// lazily constructed because contextmeter.Default() loads a tiktoken encoding,
// which is too expensive to do at package init.
var budgetTokenizer = sync.OnceValue(contextmeter.Default)

// estimateTokens returns the token cost of s using the real tokenizer rather
// than a characters-per-token approximation.
//
// This used to be len([]rune(s))/4 + 1. That heuristic is calibrated for
// English prose and undercounts badly on the content compaction actually sees:
// JSON tool input, diffs, shell output and source code all tokenize far denser
// than four characters per token. Undercounting here is not a cosmetic error —
// every consumer of this number (chunk packing, oversized-block splitting, and
// the maxFittingRunes binary search) concludes a chunk fits when it does not,
// and the resulting prompt overflows the local model's context at call time.
//
// Real tokenization costs roughly 6ms per 60K characters, which is negligible
// against a summarization pass that spends tens of seconds in the model.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return budgetTokenizer().Count(s)
}

// PackSummaryChunks splits messages so each rendered BuildSummaryPrompt(chunk)
// plus output reserve fits the configured local context window. It first packs
// on message boundaries; when one message is too large, it splits splittable
// text/tool-result blocks losslessly into same-role synthetic messages. It
// still defers rather than silently truncating an unsplittable block.
func PackSummaryChunks(messages []llm.Message, contextWindow, outputReserve int) ([][]llm.Message, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	if contextWindow <= 0 {
		return [][]llm.Message{append([]llm.Message(nil), messages...)}, nil
	}
	var chunks [][]llm.Message
	var cur []llm.Message
	for _, msg := range messages {
		candidate := append(append([]llm.Message(nil), cur...), msg)
		if EstimateSummaryBudget(BuildSummaryPrompt(candidate), outputReserve, contextWindow).Fits {
			cur = candidate
			continue
		}
		if len(cur) > 0 {
			chunks = append(chunks, cur)
			cur = nil
		}
		single := []llm.Message{msg}
		budget := EstimateSummaryBudget(BuildSummaryPrompt(single), outputReserve, contextWindow)
		if budget.Fits {
			cur = single
			continue
		}
		split, err := splitOversizedMessageForSummary(msg, contextWindow, outputReserve)
		if err != nil {
			return nil, err
		}
		for _, part := range split {
			chunks = append(chunks, []llm.Message{part})
		}
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks, nil
}

func splitOversizedMessageForSummary(msg llm.Message, contextWindow, outputReserve int) ([]llm.Message, error) {
	var out []llm.Message
	cur := llm.Message{Role: msg.Role}
	flush := func() {
		if len(cur.Blocks) > 0 {
			out = append(out, cur)
			cur = llm.Message{Role: msg.Role}
		}
	}
	for _, blk := range msg.Blocks {
		candidate := cur
		candidate.Blocks = append(append([]llm.Block(nil), cur.Blocks...), blk)
		if len(candidate.Blocks) > 0 && EstimateSummaryBudget(BuildSummaryPrompt([]llm.Message{candidate}), outputReserve, contextWindow).Fits {
			cur = candidate
			continue
		}
		flush()
		alone := llm.Message{Role: msg.Role, Blocks: []llm.Block{blk}}
		budget := EstimateSummaryBudget(BuildSummaryPrompt([]llm.Message{alone}), outputReserve, contextWindow)
		if budget.Fits {
			cur = alone
			continue
		}
		parts, err := splitOversizedBlockForSummary(msg.Role, blk, contextWindow, outputReserve)
		if err != nil {
			return nil, err
		}
		out = append(out, parts...)
	}
	flush()
	return out, nil
}

func splitOversizedBlockForSummary(role llm.Role, blk llm.Block, contextWindow, outputReserve int) ([]llm.Message, error) {
	get, set, ok := splittableSummaryText(blk)
	if !ok {
		budget := EstimateSummaryBudget(BuildSummaryPrompt([]llm.Message{{Role: role, Blocks: []llm.Block{blk}}}), outputReserve, contextWindow)
		return nil, &DeferralError{Reason: "single unsplittable message block plus summary instructions cannot fit local context", Used: budget.PromptTokens + budget.OutputReserve, Limit: contextWindow}
	}
	text := get(blk)
	if text == "" {
		budget := EstimateSummaryBudget(BuildSummaryPrompt([]llm.Message{{Role: role, Blocks: []llm.Block{blk}}}), outputReserve, contextWindow)
		return nil, &DeferralError{Reason: "empty message block plus summary instructions cannot fit local context", Used: budget.PromptTokens + budget.OutputReserve, Limit: contextWindow}
	}
	var out []llm.Message
	remaining := []rune(text)
	for len(remaining) > 0 {
		maxRunes := maxFittingRunes(role, blk, set, remaining, contextWindow, outputReserve)
		if maxRunes <= 0 {
			budget := EstimateSummaryBudget(BuildSummaryPrompt([]llm.Message{{Role: role, Blocks: []llm.Block{blk}}}), outputReserve, contextWindow)
			return nil, &DeferralError{Reason: "single message plus summary instructions cannot fit local context", Used: budget.PromptTokens + budget.OutputReserve, Limit: contextWindow}
		}
		part := blk
		set(&part, string(remaining[:maxRunes]))
		out = append(out, llm.Message{Role: role, Blocks: []llm.Block{part}})
		remaining = remaining[maxRunes:]
	}
	return out, nil
}

func splittableSummaryText(blk llm.Block) (func(llm.Block) string, func(*llm.Block, string), bool) {
	switch blk.Type {
	case llm.BlockText:
		return func(b llm.Block) string { return b.Text }, func(b *llm.Block, s string) { b.Text = s }, true
	case llm.BlockToolResult:
		return func(b llm.Block) string { return b.Content }, func(b *llm.Block, s string) { b.Content = s }, true
	default:
		return nil, nil, false
	}
}

func maxFittingRunes(role llm.Role, blk llm.Block, set func(*llm.Block, string), text []rune, contextWindow, outputReserve int) int {
	lo, hi := 1, len(text)
	best := 0
	for lo <= hi {
		mid := (lo + hi) / 2
		part := blk
		set(&part, string(text[:mid]))
		msg := llm.Message{Role: role, Blocks: []llm.Block{part}}
		if EstimateSummaryBudget(BuildSummaryPrompt([]llm.Message{msg}), outputReserve, contextWindow).Fits {
			best = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return best
}
