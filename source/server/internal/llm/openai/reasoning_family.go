package openai

import "strings"

// glmReasoningModel reports whether model is a GLM chain-of-thought release
// served over an OpenAI-compatible chat completions API (e.g. zai-org/GLM-5.3
// on DeepInfra). For these models z.ai's protocol expects the client to pin
// reasoning_effort and to return each assistant turn's reasoning_content on
// tool-call continuations; without both, hosted GLM frequently runs with
// thinking disabled and degrades into shallow refusals and malformed tool
// calls (docs/bugs/reasoning-continuation-diagnostic.md).
//
// Matching is on the published model family, not the backend: the same profile
// mechanism serves many providers, and non-GLM models must not receive the
// field. Local llama-server GLM uses chat_template_kwargs instead (see
// buildRequest) and is excluded by the caller.
func glmReasoningModel(model string) bool {
	name := model
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return strings.HasPrefix(strings.ToLower(name), "glm-")
}

// glmReasoningEffort is the pinned effort for GLM cloud calls. "high" is the
// only value documented by both DeepInfra (none|low|medium|high) and the GLM
// model card (low|high|max); the reasoning diagnostic verified it reliably
// produces reasoning where the provider default produced none.
const glmReasoningEffort = "high"
