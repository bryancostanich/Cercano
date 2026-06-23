# Project Context Initialization

## Overview
Cercano's co-processor tools (summarize, extract, classify, explain, local) previously operated with no knowledge of the project they were used in, producing generic output. This feature adds **project context** — a condensed reference document of key data structures, architecture, conventions, and domain knowledge that is automatically prepended to Cercano tool calls. The context is built collaboratively: a `cercano_init` tool scans the repo locally with local models and optionally folds in domain knowledge the host AI already has, solving the problem that local models don't know your structs/protocols/conventions and that cloud agents can't otherwise share their context with local tools.

## Design / Architecture
Context lives in a plain markdown file at `<project_root>/.cercano/context.md` — no database, no binary format, so users can read, edit, and version-control it. The host AI calls `cercano_init(project_dir, context?)`; the MCP server scans the project directory, reads key files, distills them through a local model, merges with any host-provided knowledge, and writes `context.md`. All co-processor tools then check for the file and prepend it to their prompts.

```
Host AI ──cercano_init(project_dir, context?)──► Cercano MCP Server
   cercano_init: scan repo → read README/CLAUDE.md/headers → local-model distill
                → merge host context → write .cercano/context.md → cache
   summarize / extract / classify / explain / local: load context.md, prepend
```

Key design decisions: file-based context (human-readable, editable, VC-able); collaborative building (host provides only what it already knows, Cercano does the heavy lifting locally); automatic injection (any tool call carrying `project_dir` gets context prepended, no per-call opt-in); session-level caching (loaded once into MCP server memory, re-read only on explicit re-init); and a "not initialized" nudge.

The implementation splits into a context loader (reads + caches `context.md`), a file-discovery scanner (walks the project, identifies key files by name/extension: README, CLAUDE.md, `.claude/memory/*`, `*.proto`, `*.h`, configs, Makefile, go.mod, package.json), size-aware reading (skips binaries and dependency dirs like node_modules), and a Builder (`BuildPrompt` combines files + host context, `WriteContext` writes the file).

## Key behaviors / capabilities
- **`cercano_init`** — takes required `project_dir` and optional `context` (host knowledge). Scans the repo, distills domain knowledge (data structures, APIs, architecture, conventions, important constants) via local models, merges, writes `.cercano/context.md`, and loads it into the session cache. Returns a summary of files scanned, context size, and key topics. Re-running overwrites the file and invalidates the cache.
- **Context file format** — markdown with structured sections (project overview, key data structures, APIs/protocols, architecture, conventions, file layout), targeted at ~2-4K tokens so it can be prepended without blowing local context windows.
- **Context injection** — co-processor tools gained an optional `project_dir` parameter; when provided and `context.md` exists, content is prepended as `"Project Context:\n{context}\n\n---\n\n{original prompt}"`.
- **Not-initialized nudge** — when a tool call includes `project_dir` but no `context.md` exists, the response appends a recommendation to run `cercano_init`, shown only once per session (tracked by a flag).
- **Agent Skill (SKILL.md)** — instructs the host AI to provide `project_dir`, include `context` only if it already has meaningful domain knowledge, and explicitly NOT to go research/read the project just to populate the parameter — Cercano scans the repo itself.

## Notable decisions / constraints
- The host AI is deliberately told not to over-research; if it has no context yet, providing just `project_dir` is fine.
- Out of scope: domain-specific skills (e.g. `cercano_gdb`), incremental/auto context updates on file change, multi-project context switching within one session, and embedding-based RAG retrieval (that belongs to the Semantic Search track).
- Init-event telemetry was deferred as low priority (existing tool telemetry covers basic tracking).
