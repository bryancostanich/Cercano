package anthropic

import (
	"cercano/source/server/internal/llm"
	sdk "github.com/anthropics/anthropic-sdk-go"
	"math"
)

// Keep base input separate until the inclusive total can be established. Missing
// cache fields are not presumed zero; later stream snapshots may supply them.
type usageCounts struct {
	base   llm.TokenCount
	tokens llm.TokenUsage
}

func (u usageCounts) snapshot() llm.TokenUsage {
	out := u.tokens
	if u.base.Known && out.CacheRead.Known && out.CacheWrite.Known {
		a, b, c := u.base.Value, out.CacheRead.Value, out.CacheWrite.Value
		if a <= math.MaxInt64-b && a+b <= math.MaxInt64-c {
			out.Input = llm.ReportedTokens(a + b + c)
		}
	}
	return out
}
func (u *usageCounts) message(v sdk.Usage) {
	if v.JSON.InputTokens.Valid() {
		u.base = llm.ReportedTokens(v.InputTokens)
	}
	if v.JSON.OutputTokens.Valid() {
		u.tokens.Output = llm.ReportedTokens(v.OutputTokens)
	}
	if v.JSON.CacheReadInputTokens.Valid() {
		u.tokens.CacheRead = llm.ReportedTokens(v.CacheReadInputTokens)
	}
	if v.JSON.CacheCreationInputTokens.Valid() {
		u.tokens.CacheWrite = llm.ReportedTokens(v.CacheCreationInputTokens)
	}
	if v.OutputTokensDetails.JSON.ThinkingTokens.Valid() {
		u.tokens.Reasoning = llm.ReportedTokens(v.OutputTokensDetails.ThinkingTokens)
	}
}
func (u *usageCounts) delta(v sdk.MessageDeltaUsage) {
	if v.JSON.InputTokens.Valid() {
		u.base = llm.ReportedTokens(v.InputTokens)
	}
	if v.JSON.OutputTokens.Valid() {
		u.tokens.Output = llm.ReportedTokens(v.OutputTokens)
	}
	if v.JSON.CacheReadInputTokens.Valid() {
		u.tokens.CacheRead = llm.ReportedTokens(v.CacheReadInputTokens)
	}
	if v.JSON.CacheCreationInputTokens.Valid() {
		u.tokens.CacheWrite = llm.ReportedTokens(v.CacheCreationInputTokens)
	}
	if v.OutputTokensDetails.JSON.ThinkingTokens.Valid() {
		u.tokens.Reasoning = llm.ReportedTokens(v.OutputTokensDetails.ThinkingTokens)
	}
}
