# Track Plan: Local Co-Processor Tools

## Phase 1: Tool Surface Design

### Objective
Finalize the tool surface based on real usage patterns and the local co-processor value proposition (bandwidth, privacy, cost, latency, availability, parallelism). Decide which tools to build first, define their contracts, and design prompt templates.

### Tasks
- [x] Task: Finalize tool list and priority order based on value proposition analysis.
    - [x] Rank by: frequency of use × cloud token savings × implementation complexity.
    - [x] Identify MVP set: summarize, extract.
    - [x] Full priority order: summarize > extract > explain > classify > search > boilerplate.
    - [x] Decision: boilerplate cut (cercano_local already handles it), search moved to its own track.
- [x] Task: Design input/output schemas for each MVP tool.
    - [x] Define MCP tool descriptions optimized for agent discoverability.
    - [x] Decision: reuse existing `ProcessRequest` gRPC RPC — no new proto RPCs needed. Tools are prompt-wrapping at the MCP layer.
- [x] Task: Design prompt templates for each MVP tool, tuned for local models.
    - [x] Summarize template: length-parameterized (brief/medium/detailed), "output only the summary, no preamble".
    - [x] Extract template: query-driven, "output ONLY the extracted content, no commentary".
    - [x] Tested against qwen3-coder on Mac Studio (remote Ollama) — output quality good.
- [x] Task: Conductor - User Manual Verification 'Tool Surface Design' (Protocol in workflow.md)

## Phase 2: cercano_summarize

### Objective
Build the summarize tool — condense files, diffs, logs, or arbitrary text into concise summaries suitable for cloud agent context windows.

### Tasks
- [x] Task: Decide on RPC approach — reuse `ProcessRequest` with prompt wrapping in MCP layer. [0511240]
    - [x] No new proto messages needed.
    - [x] No server-side changes needed for the tool itself.
- [x] Task: Implement `cercano_summarize` MCP tool. [0511240]
    - [x] `SummarizeRequest` struct with text, file_path, max_length fields.
    - [x] `handleSummarize` handler: validates input, reads file if needed, constructs prompt, calls ProcessRequest.
    - [x] Registered in `registerTools()` with descriptive schema.
    - [x] Red/Green TDD: 7 tests (registration, text input, file input, max_length, no input, file not found, gRPC error).
- [x] Task: Fix Ollama context overflow for long prompts. [1347c89]
    - [x] Added `num_ctx: 32768` to `generateRequest` options in OllamaProvider.
- [x] Task: Fix SmartRouter embedding overflow for long prompts. [0a739c5]
    - [x] Truncate input to 512 chars in `extractQueryText` before embedding.
    - [x] Fix: always truncate after context delimiter stripping. [648b570]
- [x] Task: End-to-end test with Claude Code — summarize a real file and verify useful output.
    - [x] Summarized `server.go` (11KB) with brief and detailed modes via Mac Studio remote.
- [x] Task: Conductor - User Manual Verification 'cercano_summarize' (Protocol in workflow.md)

## Phase 3: cercano_extract

### Objective
Build the extract tool — pull specific information from large text based on a query.

### Tasks
- [x] Task: Decide on RPC approach — reuse `ProcessRequest` with prompt wrapping in MCP layer. [0511240]
    - [x] No new proto messages needed.
- [x] Task: Implement `cercano_extract` MCP tool. [0511240]
    - [x] `ExtractRequest` struct with text, query fields.
    - [x] `handleExtract` handler: validates input, constructs prompt, calls ProcessRequest.
    - [x] Registered in `registerTools()` with descriptive schema.
    - [x] Red/Green TDD: 5 tests (registration, basic, missing text, missing query, gRPC error).
- [x] Task: End-to-end test with Claude Code — extract info from a real log.
    - [x] Extracted error/warning messages from a sample log — returned exactly the relevant lines.
- [x] Task: Conductor - User Manual Verification 'cercano_extract' (Protocol in workflow.md)

## Phase 4: cercano_classify & cercano_explain

### Objective
Build the classify and explain tools — quick local triage and code comprehension.

### Tasks
- [x] Task: Implement `cercano_classify` MCP tool. [648b570]
    - [x] Prompt template: structured output (Category/Confidence/Reasoning).
    - [x] Support custom categories or auto-determined.
    - [x] Wire to gRPC, Red/Green TDD (5 tests).
- [x] Task: Implement `cercano_explain` MCP tool. [648b570]
    - [x] Prompt template for developer-focused code explanation.
    - [x] Support file_path and text inputs.
    - [x] Wire to gRPC, Red/Green TDD (5 tests).
- [x] Task: Fix extractQueryText truncation bug — delimiter match inside file content bypassed truncation. [648b570]
- [x] Task: End-to-end tests for both tools.
    - [x] cercano_classify: classified a panic stack trace as "bug" with high confidence.
    - [x] cercano_explain: explained router.go (~14KB) — full detailed explanation returned.
- [x] Task: Fix error propagation — formatGRPCError now preserves original error message. [8921dca]
- [x] Task: Conductor - User Manual Verification 'cercano_classify & cercano_explain' (Protocol in workflow.md)

## Phase 5: Documentation & Closeout

### Objective
Update README with new tool documentation and close out the track.

### Tasks
- [ ] Task: Update README.md MCP Tools table with cercano_summarize, cercano_extract, cercano_classify, cercano_explain.
- [ ] Task: Add usage examples for the new tools.
- [ ] Task: Conductor - User Manual Verification 'Documentation & Closeout' (Protocol in workflow.md)
