package responses

import "cercano/source/server/internal/llm"

func presentCount(n *int64) llm.TokenCount {
	if n == nil {
		return llm.TokenCount{}
	}
	return llm.ReportedTokens(*n)
}
func (u *usage) normalized() llm.TokenUsage {
	if u == nil {
		return llm.TokenUsage{}
	}
	return llm.TokenUsage{Input: presentCount(u.InputTokens), Output: presentCount(u.OutputTokens), CacheRead: presentCount(u.InputDetails.CachedTokens), Reasoning: presentCount(u.OutputDetails.ReasoningTokens)}
}
