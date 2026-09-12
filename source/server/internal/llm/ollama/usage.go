package ollama

import (
	"cercano/source/server/internal/llm"
	api "github.com/ollama/ollama/api"
)

// Ollama SDK counters lose JSON presence just like the OpenAI SDK counters.
func normalizedUsage(r api.ChatResponse) llm.TokenUsage {
	var out llm.TokenUsage
	if r.PromptEvalCount > 0 {
		out.Input = llm.ReportedTokens(int64(r.PromptEvalCount))
	}
	if r.EvalCount > 0 {
		out.Output = llm.ReportedTokens(int64(r.EvalCount))
	}
	return out
}
