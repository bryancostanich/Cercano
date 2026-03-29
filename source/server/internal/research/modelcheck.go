package research

import "strings"

// codeOnlyModels are models optimized for code, not research/analysis.
var codeOnlyModels = []string{
	"qwen3-coder",
	"codellama",
	"deepseek-coder",
	"starcoder",
	"codegemma",
	"stable-code",
	"codestral",
	"granite-code",
}

// researchCapableModels are models good for analytical/research tasks, in preference order.
var researchCapableModels = []string{
	"qwen2.5",
	"qwen3",
	"llama3.1",
	"llama3",
	"gemma2",
	"gemma3",
	"deepseek-r1",
	"mistral",
	"command-r",
	"phi3",
	"phi4",
	"mixtral",
}

// IsCodeOnlyModel returns true if the model name matches a code-only model.
func IsCodeOnlyModel(model string) bool {
	lower := strings.ToLower(model)
	for _, code := range codeOnlyModels {
		if strings.Contains(lower, code) {
			return true
		}
	}
	return false
}

// SuggestResearchModel finds the best available research-capable model from a list of installed models.
// Returns the suggested model name and true if a better option exists, or empty and false if not.
func SuggestResearchModel(availableModels []string) (string, bool) {
	// Check in preference order
	for _, preferred := range researchCapableModels {
		for _, available := range availableModels {
			lower := strings.ToLower(available)
			// Match the base name (e.g. "qwen2.5" matches "qwen2.5:latest" or "qwen2.5:72b")
			if strings.Contains(lower, preferred) && !IsCodeOnlyModel(available) {
				return available, true
			}
		}
	}
	return "", false
}

// CheckResearchModel checks if the current model is appropriate for research.
// Returns a suggestion message if a better model is available, or empty string if fine.
func CheckResearchModel(currentModel string, availableModels []string) string {
	if !IsCodeOnlyModel(currentModel) {
		return "" // current model is fine
	}

	suggested, found := SuggestResearchModel(availableModels)
	if !found {
		return "" // no better option available
	}

	return "Note: You're using " + currentModel + " which is optimized for code, not research analysis. " +
		"For better results, switch with: cercano_config(action: \"set\", local_model: \"" + suggested + "\")"
}
