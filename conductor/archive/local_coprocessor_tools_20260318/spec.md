# Track Specification: Local Co-Processor Tools

## 1. Job Title
Design and build specialized MCP tools that make Cercano a high-value local co-processor for cloud-based AI agents.

## 2. Overview
Today, when a cloud agent like Claude Code needs to understand a codebase, summarize a file, or sift through logs, it sends all that data to the cloud — consuming context window, costing tokens, and potentially leaking private data. Cercano is already wired as an MCP server with a single flexible tool (`cercano_local`). This track designs and builds **specialized tools** that let cloud agents offload specific tasks to local inference, keeping the workflow fast, cheap, and private.

### Value Proposition — Why Local?
The evaluation framework for whether a task should be handled locally:

1. **Bandwidth / Context Efficiency** — Don't shove raw data to the cloud when a local model can distill it first. Cloud context windows are expensive and finite.
2. **Privacy** — Keep sensitive data (credentials, PII, proprietary code) off the wire entirely.
3. **Cost** — Local inference is free. Every token sent to Claude/Gemini costs money. Offload the grunt work.
4. **Latency** — For simple tasks, a local model responds in milliseconds vs. a cloud round-trip. Especially with powerful local hardware (Mac Studio: 100+ tok/s).
5. **Availability** — Works offline, no rate limits, no outages.
6. **Parallelism** — Cloud agents are often serialized. Local tools can crunch multiple things in parallel without competing for cloud rate limits.

**Decision rule:** If a task is high-volume, low-complexity, privacy-sensitive, or latency-sensitive, it's a good candidate for local offload.

### Proposed Tools

| Tool | Description | Value Prop |
|------|-------------|-----------|
| `cercano_summarize` | Condense a file, diff, log, or pasted text into key points | Bandwidth, cost — distill before sending to cloud |
| `cercano_extract` | Pull specific info from large text (e.g., find the error in a 500-line log) | Bandwidth, cost — filter noise locally |
| `cercano_search` | Semantic search across the codebase ("find auth-related code") | Latency, privacy — keep codebase local |
| `cercano_classify` | Triage/classify input (e.g., "is this a bug, config issue, or infra problem?") | Latency, cost — quick local triage |
| `cercano_explain` | Explain what a function/file does, answered locally for context-building | Privacy, cost — understand code without cloud |
| `cercano_boilerplate` | Generate test stubs, interface impls, repetitive code | Cost — doesn't need frontier intelligence |

**What changes:** New MCP tools backed by new or existing gRPC RPCs, new prompt templates optimized for local models, and updates to the MCP server tool registry.

**What does NOT change:** The existing `cercano_local` tool remains as the general-purpose escape hatch. The SmartRouter, agentic loop, and core server architecture stay the same.

## 3. Architecture Decision

```
Cloud Agent (Claude Code, Cursor, etc.)
    │
    │ MCP calls
    ▼
┌─────────────────────────────────┐
│       Cercano MCP Server        │
│                                 │
│  cercano_summarize ─────┐       │
│  cercano_extract ───────┤       │
│  cercano_search ────────┤ gRPC  │
│  cercano_classify ──────┤───────┼──► Cercano Server
│  cercano_explain ───────┤       │       │
│  cercano_boilerplate ───┘       │       ▼
│                                 │    Ollama (local or remote)
│  cercano_local (general) ───────┤
│  cercano_config ────────────────┤
│  cercano_models ────────────────┘
└─────────────────────────────────┘
```

Key decisions:
- **Specialized tools with clear contracts** — Each tool has a focused purpose, well-defined input schema, and predictable output format. This gives cloud agents better tool descriptions to reason about when to use local vs. cloud.
- **Prompt engineering per tool** — Each tool uses an optimized system prompt/template for the task, tuned for local model capabilities (not frontier models).
- **`cercano_local` stays** — It remains as the general-purpose tool for anything that doesn't fit a specialized tool.
- **Incremental rollout** — Tools are built one at a time, tested with real agent workflows, and iterated.

## 4. Requirements

### 4.1 cercano_summarize
- Input: `text` (raw text to summarize), or `file_path` (path to file to read and summarize), `max_length` (optional, target summary length).
- Output: Concise summary suitable for inclusion in a cloud agent's context.
- Must handle: code files, diffs, logs, documentation, arbitrary text.
- Prompt template optimized for local models.

### 4.2 cercano_extract
- Input: `text` (raw text to search), `query` (what to find/extract).
- Output: Extracted relevant sections with context.
- Use case: "Find the error in this log", "Extract the API endpoints from this file", "What config values are set?"

### 4.3 cercano_search
- Input: `query` (semantic search query), `path` (optional, scope to directory).
- Output: Ranked list of relevant files/functions with snippets.
- This requires embeddings — uses existing `nomic-embed-text` model or similar.
- Different from grep: understands intent, not just string matching.

### 4.4 cercano_classify
- Input: `text` (content to classify), `categories` (optional, list of categories to choose from).
- Output: Classification with confidence and brief reasoning.
- Default categories for common use cases (error triage, code quality, etc.) if none provided.

### 4.5 cercano_explain
- Input: `file_path` and/or `text` (code to explain), `audience` (optional — e.g., "beginner", "expert").
- Output: Explanation of what the code does, key interfaces, data flow.

### 4.6 cercano_boilerplate
- Input: `type` (what to generate — "test", "interface_impl", "struct", etc.), `context` (existing code/types to base it on).
- Output: Generated boilerplate code.

## 5. Acceptance Criteria
- [ ] Each tool is discoverable via MCP and has a clear, descriptive tool description.
- [ ] Each tool produces useful output with local models (qwen3-coder, GLM-4.7-Flash).
- [ ] Cloud agents (Claude Code) can effectively use the tools to reduce cloud token usage.
- [ ] `cercano_local` continues to work as a general-purpose fallback.
- [ ] Each tool has unit tests and has been tested end-to-end with a running server.

## 6. Out of Scope
- Agent Skills packaging (SKILL.md) — that's the Agent Skills track.
- Competitive audit research — that's the Competitive Audit track.
- Changes to the core SmartRouter or agentic loop.
- IDE extension changes (VS Code, Zed).
