package server

import (
	"context"
	"fmt"

	"cercano/source/server/pkg/proto"
)

// builtinSkill defines a built-in Cercano skill.
type builtinSkill struct {
	Name        string
	Description string
	Content     string // Full SKILL.md content
}

// builtinSkills returns the catalog of all built-in Cercano skills.
func builtinSkills() []builtinSkill {
	return []builtinSkill{
		{
			Name:        "cercano-local",
			Description: "Run prompts against local AI models via Cercano and Ollama. Use this for local inference — faster, private, and zero cost. Handles chat-style queries and agentic code generation with automatic validation.",
			Content: `---
name: cercano-local
description: Run prompts against local AI models via Cercano and Ollama. Use this for local inference — faster, private, and zero cost. Handles chat-style queries and agentic code generation with automatic validation. Offload summarization, explanation, code writing, and general LLM tasks to a local model instead of sending them to the cloud.
compatibility: Requires Cercano server running and connected to an Ollama instance.
---

# Cercano Local Inference

Run prompts against local AI models through Cercano's MCP interface.

## MCP Tool

**Tool name:** ` + "`cercano_local`" + `

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| prompt | string | Yes | The prompt to run against local models. |
| file_path | string | No | Target file path for code changes. When provided with work_dir, enables agentic code generation. |
| work_dir | string | No | Working directory for code validation. |
| context | string | No | Additional context such as existing code or file contents. |
| conversation_id | string | No | Conversation ID for multi-turn support. |
`,
		},
		{
			Name:        "cercano-models",
			Description: "List AI models available on the Ollama instance connected to Cercano. Returns model names, sizes, and modification dates.",
			Content: `---
name: cercano-models
description: List AI models available on the Ollama instance connected to Cercano. Returns model names, sizes, and modification dates. Use this to discover what local models are available before choosing one for inference or switching the active model.
compatibility: Requires Cercano server running and connected to an Ollama instance.
---

# Cercano Models

List all models available on the active Ollama instance.

## MCP Tool

**Tool name:** ` + "`cercano_models`" + `

## Parameters

None.
`,
		},
		{
			Name:        "cercano-config",
			Description: "Query or update Cercano's runtime configuration without restarting the server. Switch models, endpoints, or cloud providers.",
			Content: `---
name: cercano-config
description: Query or update Cercano's runtime configuration without restarting the server. Use this to switch the active local model, change the Ollama endpoint URL, or change the cloud provider and model.
compatibility: Requires Cercano server running.
---

# Cercano Config

Query or update Cercano's runtime configuration.

## MCP Tool

**Tool name:** ` + "`cercano_config`" + `

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| action | string | Yes | "get" or "set". |
| local_model | string | No | Local model name to set. |
| cloud_provider | string | No | Cloud provider: "google" or "anthropic". |
| cloud_model | string | No | Cloud model name. |
| ollama_url | string | No | Ollama endpoint URL. |
`,
		},
		{
			Name:        "cercano-summarize",
			Description: "Summarize text or files using local AI via Cercano without sending content to the cloud. Supports brief, medium, and detailed lengths.",
			Content: `---
name: cercano-summarize
description: Summarize text or files using local AI via Cercano without sending content to the cloud. Use this to distill large files, logs, diffs, documents, or any lengthy text into a concise summary. Supports brief, medium, and detailed summary lengths.
compatibility: Requires Cercano server running and connected to an Ollama instance.
---

# Cercano Summarize

Summarize text or files using local AI inference.

## MCP Tool

**Tool name:** ` + "`cercano_summarize`" + `

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| text | string | No* | Raw text to summarize. |
| file_path | string | No* | Path to a file to summarize. |
| max_length | string | No | "brief", "medium" (default), or "detailed". |

*One of text or file_path is required.
`,
		},
		{
			Name:        "cercano-extract",
			Description: "Extract specific information from text using local AI via Cercano. Returns only the relevant sections matching your query.",
			Content: `---
name: cercano-extract
description: Extract specific information from text using local AI via Cercano. Returns only the relevant sections matching your query. Use this to pull function signatures, error messages, config values, or other targeted info from large text without sending everything to the cloud.
compatibility: Requires Cercano server running and connected to an Ollama instance.
---

# Cercano Extract

Extract targeted information from text using local AI inference.

## MCP Tool

**Tool name:** ` + "`cercano_extract`" + `

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| text | string | Yes | The text to search through. |
| query | string | Yes | What to find or extract. |
`,
		},
		{
			Name:        "cercano-classify",
			Description: "Classify or triage text using local AI via Cercano. Returns a category, confidence level, and brief reasoning.",
			Content: `---
name: cercano-classify
description: Classify or triage text using local AI via Cercano. Returns a category, confidence level, and brief reasoning. Use this for quick local triage of errors, logs, code quality issues, or any content that needs categorization without sending it to the cloud.
compatibility: Requires Cercano server running and connected to an Ollama instance.
---

# Cercano Classify

Classify or triage text into categories using local AI inference.

## MCP Tool

**Tool name:** ` + "`cercano_classify`" + `

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| text | string | Yes | The text to classify. |
| categories | string | No | Comma-separated list of categories. If omitted, model determines categories. |
`,
		},
		{
			Name:        "cercano-explain",
			Description: "Explain code or text using local AI via Cercano. Returns a clear explanation of what the code does, its key interfaces, and data flow.",
			Content: `---
name: cercano-explain
description: Explain code or text using local AI via Cercano. Returns a clear explanation of what the code does, its key interfaces, and data flow. Use this to understand unfamiliar code locally before deciding what context to send to the cloud.
compatibility: Requires Cercano server running and connected to an Ollama instance.
---

# Cercano Explain

Explain code or text using local AI inference.

## MCP Tool

**Tool name:** ` + "`cercano_explain`" + `

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| text | string | No* | Code or text to explain. |
| file_path | string | No* | Path to a file to explain. |

*One of text or file_path is required.
`,
		},
	}
}

// ListSkills implements proto.AgentServer — returns the catalog of available Agent Skills.
func (s *Server) ListSkills(ctx context.Context, req *proto.ListSkillsRequest) (*proto.ListSkillsResponse, error) {
	skills := builtinSkills()
	protoSkills := make([]*proto.SkillInfo, len(skills))
	for i, sk := range skills {
		protoSkills[i] = &proto.SkillInfo{
			Name:        sk.Name,
			Description: sk.Description,
		}
	}
	return &proto.ListSkillsResponse{Skills: protoSkills}, nil
}

// GetSkill implements proto.AgentServer — returns the full SKILL.md content for a specific skill.
func (s *Server) GetSkill(ctx context.Context, req *proto.GetSkillRequest) (*proto.GetSkillResponse, error) {
	for _, sk := range builtinSkills() {
		if sk.Name == req.Name {
			return &proto.GetSkillResponse{
				Name:    sk.Name,
				Content: sk.Content,
			}, nil
		}
	}
	return nil, fmt.Errorf("skill %q not found", req.Name)
}
