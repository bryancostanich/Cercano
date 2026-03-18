# Track Specification: Semantic Codebase Search

## 1. Job Title
Build a semantic codebase search tool that finds relevant code by intent, not just string matching.

## 2. Overview
Add a `cercano_search` MCP tool that lets cloud agents search a codebase semantically — "find auth-related code" rather than grepping for "auth". This requires an embedding-based indexing pipeline, storage, and nearest-neighbor retrieval, making it architecturally distinct from the other co-processor tools (which are single-shot prompt wrappers).

### Value Proposition
- **Privacy** — Codebase stays local, never sent to cloud for search
- **Latency** — Sub-second search once indexed, vs. cloud round-trip
- **Semantic understanding** — Finds conceptually related code, not just keyword matches

## 3. Open Design Questions
These need to be resolved before implementation:

- **Indexing trigger** — When to index? On first search? On server startup? On file change (fsnotify)?
- **Incremental vs. full** — Re-index only changed files, or rebuild the full index?
- **Storage format** — In-memory? SQLite? Flat file? Needs to persist across server restarts.
- **Chunking strategy** — How to split files? By function? By fixed token count? By semantic boundaries?
- **Embedding model** — `nomic-embed-text` (already available) has a small context window (~2K tokens). Is that sufficient per chunk?
- **Scope** — Search the whole project? A configurable directory? Multiple roots?

## 4. Requirements
- `cercano_search(query: "auth middleware", path: "/optional/scope")` returns ranked results with file paths, snippets, and relevance scores.
- Must handle projects with hundreds of files without excessive indexing time.
- Must work with the existing Ollama embedding infrastructure.

## 5. Out of Scope
- Real-time file watching (can be added later)
- Cross-repository search
- Non-code content (images, binaries)
