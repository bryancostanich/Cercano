# Track Plan: Local Co-Processor Tools

## Phase 1: Tool Surface Design

### Objective
Finalize the tool surface based on real usage patterns and the local co-processor value proposition (bandwidth, privacy, cost, latency, availability, parallelism). Decide which tools to build first, define their contracts, and design prompt templates.

### Tasks
- [x] Task: Finalize tool list and priority order based on value proposition analysis.
    - [x] Rank by: frequency of use × cloud token savings × implementation complexity.
    - [x] Identify MVP set: summarize, extract.
    - [x] Full priority order: summarize > extract > explain > classify > search > boilerplate.
- [x] Task: Design input/output schemas for each MVP tool.
    - [x] Define MCP tool descriptions optimized for agent discoverability.
    - [x] Decision: reuse existing `ProcessRequest` gRPC RPC — no new proto RPCs needed. Tools are prompt-wrapping at the MCP layer.
- [x] Task: Design prompt templates for each MVP tool, tuned for local models.
    - [x] Summarize template: length-parameterized (brief/medium/detailed), "output only the summary, no preamble".
    - [x] Extract template: query-driven, "output ONLY the extracted content, no commentary".
    - [x] Tested against qwen3-coder on Mac Studio (remote Ollama) — output quality good.
- [ ] Task: Conductor - User Manual Verification 'Tool Surface Design' (Protocol in workflow.md)

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
    - [x] Truncate input to 2048 chars in `extractQueryText` before embedding — only the beginning is needed for intent classification.
- [x] Task: End-to-end test with Claude Code — summarize a real file and verify useful output.
    - [x] Summarized `server.go` (11KB) with brief and detailed modes via Mac Studio remote.
- [ ] Task: Conductor - User Manual Verification 'cercano_summarize' (Protocol in workflow.md)

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
- [ ] Task: Conductor - User Manual Verification 'cercano_extract' (Protocol in workflow.md)

## Phase 4: cercano_search (Semantic)

### Objective
Build semantic codebase search — find relevant code by intent, not just string matching.

### Tasks
- [ ] Task: Design the indexing strategy (when to index, incremental vs. full, storage format).
- [ ] Task: Implement codebase indexing using embeddings (nomic-embed-text or similar).
    - [ ] Walk directory, chunk files, generate embeddings.
    - [ ] Store embeddings for fast retrieval.
    - [ ] Red/Green TDD.
- [ ] Task: Implement semantic search query.
    - [ ] Embed the query, find nearest neighbors.
    - [ ] Return ranked results with file paths and snippets.
    - [ ] Red/Green TDD.
- [ ] Task: Add `cercano_search` MCP tool.
    - [ ] Register tool with descriptive schema.
    - [ ] Wire to gRPC.
    - [ ] Red/Green TDD.
- [ ] Task: End-to-end test with Claude Code — semantic search on a real codebase.
- [ ] Task: Conductor - User Manual Verification 'cercano_search' (Protocol in workflow.md)

## Phase 5: cercano_classify & cercano_explain

### Objective
Build the classify and explain tools — quick local triage and code comprehension.

### Tasks
- [ ] Task: Implement `cercano_classify` MCP tool.
    - [ ] Design prompt template for classification.
    - [ ] Support custom categories or default set.
    - [ ] Wire to gRPC, Red/Green TDD.
- [ ] Task: Implement `cercano_explain` MCP tool.
    - [ ] Design prompt template for code explanation.
    - [ ] Support file_path and text inputs, optional audience level.
    - [ ] Wire to gRPC, Red/Green TDD.
- [ ] Task: End-to-end tests for both tools.
- [ ] Task: Conductor - User Manual Verification 'cercano_classify & cercano_explain' (Protocol in workflow.md)

## Phase 6: cercano_boilerplate & Integration

### Objective
Build the boilerplate generator and run final integration testing across all tools.

### Tasks
- [ ] Task: Implement `cercano_boilerplate` MCP tool.
    - [ ] Design prompt template for boilerplate generation.
    - [ ] Support type parameter (test, interface_impl, struct, etc.).
    - [ ] Wire to gRPC, Red/Green TDD.
- [ ] Task: Integration test — use all tools in a realistic Claude Code workflow.
- [ ] Task: Update README.md with new tool documentation.
- [ ] Task: Conductor - User Manual Verification 'cercano_boilerplate & Integration' (Protocol in workflow.md)
