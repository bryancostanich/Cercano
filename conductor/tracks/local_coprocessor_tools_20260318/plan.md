# Track Plan: Local Co-Processor Tools

## Phase 1: Tool Surface Design

### Objective
Finalize the tool surface based on real usage patterns and competitive audit findings. Decide which tools to build first, define their contracts, and design prompt templates.

### Tasks
- [ ] Task: Review competitive audit findings (if available) for tool surface inspiration.
- [ ] Task: Finalize tool list and priority order based on value proposition analysis.
    - [ ] Rank by: frequency of use × cloud token savings × implementation complexity.
    - [ ] Identify MVP set (likely: summarize, extract).
- [ ] Task: Design input/output schemas for each MVP tool.
    - [ ] Define MCP tool descriptions optimized for agent discoverability.
    - [ ] Define gRPC message types if new RPCs are needed vs. reusing ProcessRequest.
- [ ] Task: Design prompt templates for each MVP tool, tuned for local models.
    - [ ] Test templates manually against qwen3-coder and GLM-4.7-Flash.
    - [ ] Iterate until output quality is consistently useful.
- [ ] Task: Conductor - User Manual Verification 'Tool Surface Design' (Protocol in workflow.md)

## Phase 2: cercano_summarize

### Objective
Build the summarize tool — condense files, diffs, logs, or arbitrary text into concise summaries suitable for cloud agent context windows.

### Tasks
- [ ] Task: Add `Summarize` RPC to `agent.proto` (or decide to reuse `Process` with a mode flag).
    - [ ] Define request/response message types.
    - [ ] Regenerate Go bindings.
- [ ] Task: Implement summarize logic in the server.
    - [ ] Apply the summarize prompt template.
    - [ ] Support text input and file_path input.
    - [ ] Handle max_length parameter.
    - [ ] Red/Green TDD.
- [ ] Task: Add `cercano_summarize` MCP tool.
    - [ ] Register tool with descriptive schema.
    - [ ] Wire to gRPC.
    - [ ] Red/Green TDD.
- [ ] Task: End-to-end test with Claude Code — summarize a real file and verify useful output.
- [ ] Task: Conductor - User Manual Verification 'cercano_summarize' (Protocol in workflow.md)

## Phase 3: cercano_extract

### Objective
Build the extract tool — pull specific information from large text based on a query.

### Tasks
- [ ] Task: Add `Extract` RPC or reuse `Process` with mode routing.
    - [ ] Define request/response message types.
    - [ ] Regenerate Go bindings if needed.
- [ ] Task: Implement extract logic in the server.
    - [ ] Apply the extract prompt template.
    - [ ] Support text input and query parameter.
    - [ ] Red/Green TDD.
- [ ] Task: Add `cercano_extract` MCP tool.
    - [ ] Register tool with descriptive schema.
    - [ ] Wire to gRPC.
    - [ ] Red/Green TDD.
- [ ] Task: End-to-end test with Claude Code — extract info from a real log/file.
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
