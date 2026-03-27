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
		{
			Name:        "cercano-fetch",
			Description: "Fetch a URL and extract readable text content locally. Returns full extracted text (not a summary) — HTML is stripped to clean plain text.",
			Content: `---
name: cercano-fetch
description: Fetch a URL and extract readable text content locally. Returns full extracted text (not a summary) — HTML is stripped to clean plain text. Use this to read web pages, documentation, articles, or any URL without sending content to the cloud.
compatibility: Requires Cercano server running.
---

# Cercano Fetch

Fetch a URL and extract readable text content locally.

## MCP Tool

**Tool name:** ` + "`cercano_fetch`" + `

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| url | string | Yes | The URL to fetch. |
| project_dir | string | No | Project root directory for context-aware responses. |

## Output

Returns the full extracted text from the page. HTML is converted to plain text with structure preserved (headings, paragraphs, lists, code blocks). Scripts, styles, navigation, headers, footers, and sidebars are stripped.
`,
		},
		{
			Name:        "cercano-research",
			Description: "Research a question using DuckDuckGo search and local AI analysis. Crafts search queries, fetches top results, and synthesizes a sourced answer — all locally.",
			Content: `---
name: cercano-research
description: Research a question using DuckDuckGo search and local AI analysis. Crafts search queries, fetches top results, and synthesizes a sourced answer — all locally. Use this instead of browsing the web yourself to save cloud context tokens.
compatibility: Requires Cercano server running and Python venv set up (run 'cercano setup').
---

# Cercano Research

Research a question using web search and local AI analysis.

## MCP Tool

**Tool name:** ` + "`cercano_research`" + `

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| query | string | Yes | The research question to investigate. |
| max_results | int | No | Maximum pages to fetch and analyze (default 5). |
| project_dir | string | No | Project root directory for context-aware responses. |

## Pipeline

1. Local model generates 2-3 search queries from your question
2. DuckDuckGo searches run concurrently
3. Duplicate URLs removed
4. Top N pages fetched and converted to plain text
5. Local model analyzes fetched content and produces a sourced answer

## Prerequisites

Requires the Python venv with the ddgs package. Run ` + "`cercano setup`" + ` to create it automatically.
`,
		},
		{
			Name:        "cercano-document",
			Description: "Generate doc comments for exported Go symbols using local AI. Reads the file, generates comments, writes them back — the host never sees the file contents. Saves cloud tokens on documentation tasks.",
			Content: `---
name: cercano-document
description: Generate doc comments for exported Go symbols using local AI and write them directly to the file. The host never sees the file contents — Cercano handles the entire read-think-write cycle locally.
compatibility: Requires Cercano server running and connected to an Ollama instance. Currently supports Go source files only.
---

# Cercano Document

Generate doc comments for exported Go symbols using local AI and write them directly to the source file.

## MCP Tool

**Tool name:** ` + "`cercano_document`" + `

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| file_path | string | Yes | Path to the Go source file to document. |
| style | string | No | Doc comment style: "minimal" (1-2 sentences, default) or "detailed" (multi-line with params). |
| dry_run | bool | No | If true, report what would be documented without writing changes. |
| project_dir | string | No | Project root directory for context-aware responses. |

## How It Works

1. Parses the Go file using the standard go/ast package
2. Identifies exported symbols (functions, methods, types, interfaces, constants) without doc comments
3. Generates a doc comment for each using local inference (one symbol at a time)
4. Inserts comments at the correct positions and formats with gofmt
5. Returns a summary of what was documented

The host agent never sees the file contents — only the summary.

## Safety

- Creates a backup in .cercano/backups/ before writing
- Validates the result with go/format
- Restores from backup if validation fails
- Skips symbols where the model returns garbage

## Examples

**Document a file:**
` + "```json" + `
{"file_path": "internal/engine/ollama.go"}
` + "```" + `

**Preview without writing:**
` + "```json" + `
{"file_path": "internal/engine/ollama.go", "dry_run": true}
` + "```" + `

**Detailed style:**
` + "```json" + `
{"file_path": "internal/engine/ollama.go", "style": "detailed"}
` + "```" + `
`,
		},
		{
			Name:        "cercano-deep-research",
			Description: "Deep multi-source research tool. Takes a topic and intent, identifies authoritative sources, systematically searches, analyzes and ranks findings, chases cited references, and compiles a structured report.",
			Content: `---
name: cercano-deep-research
description: Deep multi-source research with ranked, annotated findings, reference chasing, contradiction detection, and gap analysis.
compatibility: Requires Cercano server running, connected to an Ollama instance, and Python venv with ddgs package.
---

# Cercano Deep Research

Multi-source research tool that compiles a ranked, annotated encyclopedia of findings.

## MCP Tool

**Tool name:** ` + "`cercano_deep_research`" + `

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| topic | string | Yes | The research topic |
| intent | string | Yes | What you need this research for |
| depth | string | No | survey (quick) or thorough (deep, default) |
| date_range | string | No | Filter by date (e.g. "2024-2026") |
| sources | string[] | No | Override auto-detected sources |
| output_path | string | No | Write report to file |

## Examples

` + "```json" + `
{"topic": "CRISPR sickle cell", "intent": "grant proposal", "depth": "thorough", "output_path": "/tmp/research.md"}
` + "```" + `
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
