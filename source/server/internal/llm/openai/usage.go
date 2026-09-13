package openai

import (
	"cercano/source/server/internal/llm"
	goopenai "github.com/sashabaranov/go-openai"
)

// The current SDK loses JSON field presence. Positive counters are unambiguous;
// zero may mean omitted or reported zero, so conservatively keep it unknown.
// Never replace missing actual usage with the separate context token estimate.
func sdkReportedCount(n int) llm.TokenCount {
	if n <= 0 {
		return llm.TokenCount{}
	}
	return llm.ReportedTokens(int64(n))
}

func normalizedUsage(v goopenai.Usage) llm.TokenUsage {
	out := llm.TokenUsage{Input: sdkReportedCount(v.PromptTokens), Output: sdkReportedCount(v.CompletionTokens)}
	if v.PromptTokensDetails != nil {
		out.CacheRead = sdkReportedCount(v.PromptTokensDetails.CachedTokens)
	}
	if v.CompletionTokensDetails != nil {
		out.Reasoning = sdkReportedCount(v.CompletionTokensDetails.ReasoningTokens)
	}

	// Prompt/completion totals already include these breakdowns.
	return out
}

// A non-streaming response supplies final usage, even when counts are incomplete.
func finalUsage(v goopenai.Usage) llm.TokenUsage {
	out := normalizedUsage(v)
	out.Final = out.Reported()
	return out
}
