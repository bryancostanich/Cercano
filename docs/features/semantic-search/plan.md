# Semantic Codebase Search

> Status: Planned — not yet built. Migrated from `conductor/tracks/semantic_search_20260318/`.

## Overview / Goal

Build a semantic codebase search tool that finds relevant code by intent, not just string matching.

Add a `cercano_search` MCP tool that lets cloud agents search a codebase semantically — "find auth-related code" rather than grepping for "auth". This requires an embedding-based indexing pipeline, storage, and nearest-neighbor retrieval, making it architecturally distinct from the other co-processor tools (which are single-shot prompt wrappers).

### Value Proposition
- **Privacy** — Codebase stays local, never sent to cloud for search
- **Latency** — Sub-second search once indexed, vs. cloud round-trip
- **Semantic understanding** — Finds conceptually related code, not just keyword matches

## Design / Approach

### Requirements
- `cercano_search(query: "auth middleware", path: "/optional/scope")` returns ranked results with file paths, snippets, and relevance scores.
- Must handle projects with hundreds of files without excessive indexing time.
- Must work with the existing Ollama embedding infrastructure.

### Open Design Questions (resolve before implementation)
- **Indexing trigger** — When to index? On first search? On server startup? On file change (fsnotify)?
- **Incremental vs. full** — Re-index only changed files, or rebuild the full index?
- **Storage format** — In-memory? SQLite? Flat file? Needs to persist across server restarts.
- **Chunking strategy** — How to split files? By function? By fixed token count? By semantic boundaries?
- **Embedding model** — `nomic-embed-text` (already available) has a small context window (~2K tokens). Is that sufficient per chunk?
- **Scope** — Search the whole project? A configurable directory? Multiple roots?

### Out of Scope
- Real-time file watching (can be added later)
- Cross-repository search
- Non-code content (images, binaries)

### Future Consideration
Documentation site indexing — crawl a doc site once, index it persistently, make it searchable across sessions (similar to Cursor's @Docs). This is a natural extension of the embedding/indexing infrastructure built in this track. May warrant its own phase or separate track.

## Plan / Tasks

### Phase 1: Design & Architecture

Objective: Resolve open design questions and produce an architecture document before writing code.

- [ ] Task: Decide indexing strategy (trigger, incremental vs. full, storage format).
- [ ] Task: Decide chunking strategy (function-level, fixed-size, semantic boundaries).
- [ ] Task: Evaluate embedding model context limits and chunking implications.
- [ ] Task: Design the gRPC interface (new RPC or reuse ProcessRequest).
- [ ] Task: Write architecture decision document.
- [ ] Task: Conductor - User Manual Verification 'Design & Architecture' (Protocol in workflow.md)

### Phase 2: Indexing Pipeline

Objective: Build the codebase indexing pipeline — walk files, chunk, embed, store.

- [ ] Task: Implement file walker with configurable root and ignore patterns.
- [ ] Task: Implement chunking logic.
- [ ] Task: Implement embedding generation for chunks.
- [ ] Task: Implement index storage and retrieval.
- [ ] Task: Red/Green TDD for all components.
- [ ] Task: Conductor - User Manual Verification 'Indexing Pipeline' (Protocol in workflow.md)

### Phase 3: Search & MCP Tool

Objective: Build the search query engine and expose it as an MCP tool.

- [ ] Task: Implement nearest-neighbor search over stored embeddings.
- [ ] Task: Add `cercano_search` MCP tool.
- [ ] Task: Red/Green TDD.
- [ ] Task: End-to-end test with Claude Code — semantic search on a real codebase.
- [ ] Task: Update README.md with search tool documentation.
- [ ] Task: Conductor - User Manual Verification 'Search & MCP Tool' (Protocol in workflow.md)

## Open Questions / Notes

The Phase 1 open design questions above are the primary blockers — indexing trigger, incremental vs. full reindex, storage format, chunking strategy, embedding model context sufficiency, and search scope must all be resolved before implementation begins.
