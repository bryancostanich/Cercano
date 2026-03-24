# Track Specification: Project Context Initialization

## 1. Job Title
Enable Cercano to automatically build and use project-specific context, making local inference dramatically more useful for domain-specific work.

## 2. Overview
Today, Cercano's co-processor tools (summarize, extract, classify, explain) operate without any knowledge of the project they're being used in. A call to `cercano_explain` on ARM disassembly gives generic output because the local model doesn't know what `config+0x30` means in this specific codebase. A call to `cercano_extract` on a log file doesn't know which error patterns matter for this project.

This track adds **project context** — a condensed reference document containing key data structures, architecture, conventions, and domain knowledge — that gets automatically prepended to all Cercano tool calls. The context is built collaboratively: a `cercano_init` tool scans the repo locally and optionally incorporates knowledge the host AI already has.

### The Problem
- Local models are generic — they don't know your structs, protocols, or conventions
- Cloud agents (Claude Code, Cursor) often have project context but can't share it with local tools
- Users shouldn't have to manually write context files — the tooling should build it

### The Solution
- A `.cercano/context.md` file at the project root containing distilled project knowledge
- A `cercano_init` MCP tool that builds this file by scanning the repo with local models
- The host AI can optionally provide domain knowledge it already has — but is explicitly told not to go research the project just for this
- All existing co-processor tools automatically prepend this context when available
- A "not initialized" nudge on first tool call suggesting the user run init

## 3. Architecture Decision

```
Host AI (Claude Code, Cursor)
    │
    │ 1. Calls cercano_init(project_dir, context?)
    │
    ▼
┌─────────────────────────────────────────┐
│           Cercano MCP Server            │
│                                         │
│  cercano_init                           │
│    ├── Scan project_dir for key files   │
│    ├── Read README, CLAUDE.md, headers  │
│    ├── Feed through local model         │
│    │   (summarize, extract key info)    │
│    ├── Merge with host-provided context │
│    └── Write .cercano/context.md        │
│                                         │
│  cercano_summarize ──┐                  │
│  cercano_extract ────┤ All tools check  │
│  cercano_classify ───┤ for context.md   │
│  cercano_explain ────┤ and prepend it   │
│  cercano_local ──────┘ to prompts       │
└─────────────────────────────────────────┘
```

Key decisions:
- **File-based context** — `.cercano/context.md` is a plain markdown file. No database, no binary format. Users can read, edit, or version-control it.
- **Collaborative building** — The host AI provides what it knows (if anything), Cercano does the heavy lifting locally. The skill explicitly tells the host not to go research the project.
- **Automatic injection** — Once context exists, all tool calls that include a `project_dir` parameter automatically get it prepended. No opt-in per call.
- **"Not initialized" nudge** — When a tool sees a project dir but no `.cercano/context.md`, it appends a suggestion to the response. The host AI surfaces this to the user.
- **Session-level caching** — Context is loaded once and cached in the MCP server's memory. Re-read only on explicit re-init.

## 4. Requirements

### 4.1 cercano_init
- Input: `project_dir` (required — project root path), `context` (optional — any domain knowledge the host AI already has).
- Behavior:
  1. Scan `project_dir` for key files (README, CLAUDE.md, .claude/memory/*, *.proto, *.h, config files, go.mod, package.json, Makefile, etc.)
  2. Read each file (up to size limit), skip binaries and dependency directories
  3. Use local models to summarize/extract key domain knowledge: data structures, APIs, architecture, conventions, important constants
  4. Merge local model output with host-provided context (if any)
  5. Write `.cercano/context.md`
  6. Load context into session cache
- Output: Summary of what was found and written (files scanned, context size, key topics identified).
- Re-init: If `.cercano/context.md` already exists, rebuild it (overwrite).

### 4.2 Context File Format
- Location: `<project_root>/.cercano/context.md`
- Format: Markdown with structured sections
- Expected sections: Project overview, key data structures, APIs/protocols, architecture, conventions, file layout
- Size target: concise enough to prepend without blowing local model context windows (~2-4K tokens)

### 4.3 Context Injection
- All co-processor tools gain an optional `project_dir` parameter
- When `project_dir` is provided and `.cercano/context.md` exists, its contents are prepended to the prompt
- Prepend format: `"Project Context:\n{context}\n\n---\n\n{original prompt}"`
- Context is cached in the MCP Server after first load

### 4.4 Not-Initialized Nudge
- When a tool call includes `project_dir` but no `.cercano/context.md` exists:
  - Append to the tool response: a note recommending `cercano_init` for better results
  - Only append once per session (track with a flag)

### 4.5 Agent Skill (SKILL.md)
- The `cercano_init` skill definition must explicitly instruct the host AI:
  - Provide `project_dir` (the working directory / repo root)
  - Only include `context` if you already have meaningful domain knowledge about this project
  - Do NOT go read files or research the project just to populate the context parameter — Cercano will scan the repo itself
  - If you don't have context yet, that's fine — just provide the project_dir

## 5. Acceptance Criteria
- [ ] `cercano_init` successfully scans a real project and produces a useful `context.md`
- [ ] Co-processor tools produce noticeably better output when context is loaded vs. without
- [ ] The nudge appears on first tool call without init, and doesn't repeat
- [ ] Host AI correctly provides only existing knowledge (doesn't over-research)
- [ ] Context file is human-readable and version-controllable
- [ ] Works with projects of varying sizes and languages

## 6. Out of Scope
- Domain-specific skills (e.g., `cercano_gdb`) — evaluate separately later
- Incremental context updates (e.g., auto-updating when files change) — future enhancement
- Multi-project context (switching between projects in one session) — future enhancement
- Embedding-based context retrieval (RAG) — that's the Semantic Search track
