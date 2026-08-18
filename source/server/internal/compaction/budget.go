package compaction

import (
	"fmt"

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

func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return len([]rune(s))/4 + 1
}

// PackSummaryChunks splits messages on message boundaries so each rendered
// BuildSummaryPrompt(chunk) plus output reserve fits the configured local
// context window. It never truncates a message silently.
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
		if !budget.Fits {
			return nil, &DeferralError{Reason: "single message plus summary instructions cannot fit local context", Used: budget.PromptTokens + budget.OutputReserve, Limit: contextWindow}
		}
		cur = single
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks, nil
}
