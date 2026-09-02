package compaction

import (
	"context"

	"cercano/source/server/internal/llm"
)

type SummaryCall func(ctx context.Context, prompt string, maxTokens int) (StructuredSummary, error)

type BudgetedSummaryStats struct {
	Chunks            int
	Merged            bool
	PromptTokens      []int
	ToolResultsElided int
}

// SummarizeBudgetedLocal summarizes messages using only local calls that fit
// the supplied context window. It chunks on message boundaries and combines
// parsed chunk summaries deterministically, avoiding an extra merge-model call
// that could itself overflow tight local contexts.
func SummarizeBudgetedLocal(ctx context.Context, messages []llm.Message, contextWindow, outputReserve int, call SummaryCall) (StructuredSummary, BudgetedSummaryStats, error) {
	if outputReserve <= 0 {
		outputReserve = DefaultSummaryOutputReserve
	}
	messages, deduped := ElideSupersededToolResults(messages)
	messages, lossyElided := KeepLastNToolResults(messages, DefaultLossyElisionKeepLast)
	chunks, err := PackSummaryChunks(messages, contextWindow, outputReserve)
	if err != nil {
		return StructuredSummary{}, BudgetedSummaryStats{ToolResultsElided: deduped + lossyElided}, err
	}
	stats := BudgetedSummaryStats{Chunks: len(chunks), ToolResultsElided: deduped + lossyElided}
	if len(chunks) == 0 {
		return StructuredSummary{}, stats, nil
	}
	summaries := make([]StructuredSummary, 0, len(chunks))
	for _, chunk := range chunks {
		prompt := BuildSummaryPrompt(chunk)
		budget := EstimateSummaryBudget(prompt, outputReserve, contextWindow)
		stats.PromptTokens = append(stats.PromptTokens, budget.PromptTokens)
		summary, err := call(ctx, prompt, outputReserve)
		if err != nil {
			return StructuredSummary{}, stats, err
		}
		summaries = append(summaries, summary)
	}
	if len(summaries) == 1 {
		return summaries[0], stats, nil
	}
	stats.Merged = true
	return MergeSummaries(summaries), stats, nil
}
