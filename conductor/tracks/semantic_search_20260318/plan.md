# Track Plan: Semantic Codebase Search

## Phase 1: Design & Architecture

### Objective
Resolve open design questions and produce an architecture document before writing code.

### Tasks
- [ ] Task: Decide indexing strategy (trigger, incremental vs. full, storage format).
- [ ] Task: Decide chunking strategy (function-level, fixed-size, semantic boundaries).
- [ ] Task: Evaluate embedding model context limits and chunking implications.
- [ ] Task: Design the gRPC interface (new RPC or reuse ProcessRequest).
- [ ] Task: Write architecture decision document.
- [ ] Task: Conductor - User Manual Verification 'Design & Architecture' (Protocol in workflow.md)

## Phase 2: Indexing Pipeline

### Objective
Build the codebase indexing pipeline — walk files, chunk, embed, store.

### Tasks
- [ ] Task: Implement file walker with configurable root and ignore patterns.
- [ ] Task: Implement chunking logic.
- [ ] Task: Implement embedding generation for chunks.
- [ ] Task: Implement index storage and retrieval.
- [ ] Task: Red/Green TDD for all components.
- [ ] Task: Conductor - User Manual Verification 'Indexing Pipeline' (Protocol in workflow.md)

## Phase 3: Search & MCP Tool

### Objective
Build the search query engine and expose it as an MCP tool.

### Tasks
- [ ] Task: Implement nearest-neighbor search over stored embeddings.
- [ ] Task: Add `cercano_search` MCP tool.
- [ ] Task: Red/Green TDD.
- [ ] Task: End-to-end test with Claude Code — semantic search on a real codebase.
- [ ] Task: Update README.md with search tool documentation.
- [ ] Task: Conductor - User Manual Verification 'Search & MCP Tool' (Protocol in workflow.md)
