# Local Co-Processor Tools

## Overview
This feature added specialized MCP tools that turn Cercano into a high-value local co-processor for cloud agents (Claude Code, Cursor, etc.). Instead of shoving raw files, diffs, and logs into an expensive cloud context window, a cloud agent can offload distillation and comprehension to local inference — keeping work fast, cheap, and private. The shipped set is `cercano_summarize`, `cercano_extract`, `cercano_classify`, and `cercano_explain`, alongside the pre-existing general-purpose `cercano_local`.

## Design / Architecture
The tools are implemented as MCP handlers in the MCP server, registered in `registerTools()` with descriptive, discoverable schemas. A key decision: all four reuse the existing `ProcessRequest` gRPC RPC — no new proto messages or server-side changes were needed. Each tool is essentially a tuned prompt-wrapping layer at the MCP level, with a system prompt template optimized for local models (qwen3-coder, GLM-4.7-Flash) rather than frontier models.

- **`cercano_summarize`** — `SummarizeRequest{text, file_path, max_length}`. `handleSummarize` validates input, reads the file if a path is given, builds a length-parameterized prompt (brief/medium/detailed, "output only the summary, no preamble"), and calls `ProcessRequest`. Handles code, diffs, logs, docs, arbitrary text.
- **`cercano_extract`** — `ExtractRequest{text, query}`. Query-driven extraction ("output ONLY the extracted content, no commentary"). Use cases: find the error in a log, pull API endpoints, list config values.
- **`cercano_classify`** — Triage/classify input with structured output (Category / Confidence / Reasoning); supports custom or auto-determined categories.
- **`cercano_explain`** — Developer-focused code explanation; accepts `file_path` and/or `text`.

The general-purpose `cercano_local` remains the escape hatch; the SmartRouter, agentic loop, and core architecture were unchanged.

## Key Behaviors / Capabilities
- Each tool is MCP-discoverable with a clear description so agents know when to offload locally vs. use the cloud.
- All tools backed by local inference (free, private, low-latency, offline-capable).
- Verified end-to-end with Claude Code: summarized `server.go` (11KB) brief and detailed; extracted exactly the relevant error/warning lines from a log; classified a panic stack trace as "bug" with high confidence; explained `router.go` (~14KB) in full.
- Each tool carries unit tests (TDD red/green: 7 for summarize, 5 each for extract/classify/explain).

## Notable Decisions / Constraints
- Value-prop / decision rule: offload tasks that are high-volume, low-complexity, privacy-sensitive, or latency-sensitive (bandwidth, privacy, cost, latency, availability, parallelism).
- Prioritization: summarize > extract > explain > classify > search > boilerplate. `boilerplate` was cut (`cercano_local` already covers it); `cercano_search` (semantic codebase search) was moved to its own track.
- Fixes shipped alongside the tools: Ollama context overflow on long prompts (`num_ctx: 32768` in `generateRequest`); SmartRouter embedding overflow (truncate input to 512 chars in `extractQueryText`, always after context-delimiter stripping — a delimiter match inside file content previously bypassed truncation); error propagation (`formatGRPCError` now preserves the original error message).
- Out of scope: Agent Skills (SKILL.md) packaging, competitive-audit research, SmartRouter/agentic-loop changes, and IDE extension changes.
