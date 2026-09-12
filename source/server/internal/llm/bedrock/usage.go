package bedrock

import (
	"cercano/source/server/internal/llm"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func bedrockCount(n *int32) llm.TokenCount {
	if n == nil {
		return llm.TokenCount{}
	}
	return llm.ReportedTokens(int64(*n))
}

// AWS documents total input = inputTokens + cacheReadInputTokens +
// cacheWriteInputTokens when caching is enabled:
// https://docs.aws.amazon.com/bedrock/latest/userguide/prompt-caching.html
// This adapter sends no cache checkpoints. If neither optional cache field is
// reported, inputTokens is the uncached-request total. A partially reported
// cache breakdown leaves the inclusive total unknown rather than guessing zero.
func normalizedUsage(v *types.TokenUsage) llm.TokenUsage {
	if v == nil {
		return llm.TokenUsage{}
	}
	out := llm.TokenUsage{Input: bedrockCount(v.InputTokens), Output: bedrockCount(v.OutputTokens), CacheRead: bedrockCount(v.CacheReadInputTokens), CacheWrite: bedrockCount(v.CacheWriteInputTokens)}
	if v.CacheReadInputTokens != nil || v.CacheWriteInputTokens != nil {
		base := out.Input
		out.Input = llm.TokenCount{}
		if base.Known && out.CacheRead.Known && out.CacheWrite.Known {
			out.Input = llm.ReportedTokens(base.Value + out.CacheRead.Value + out.CacheWrite.Value)
		}
	}
	return out
}
