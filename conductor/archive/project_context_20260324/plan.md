# Track Plan: Project Context Initialization

## Phase 1: Design & Architecture

### Objective
Design the project context system — how context is built, stored, loaded, and injected into tool calls.

### Tasks
- [x] Task: Define the context file format and location (`.cercano/context.md` at project root).
- [x] Task: Design the `cercano_init` tool interface — required params (`project_dir`), optional params (`context` from host AI), return value.
- [x] Task: Design the repo scanning strategy — which files to read (README, CLAUDE.md, memory files, headers, proto files, configs), size limits, prioritization.
- [x] Task: Design the local model summarization pipeline — how scanned files get distilled into a concise context document.
- [x] Task: Design how the skill prompt instructs the host AI — explicit guidance that the host should only provide context it already has, not go research the project.
- [x] Task: Design context injection — how existing tools (summarize, extract, classify, explain, local) prepend context to prompts. Session-level caching vs. per-call file read.
- [x] Task: Write architecture decision document — covered by spec.md.
- [-] Task: Conductor - User Manual Verification 'Design & Architecture' *(deferred — spec.md reviewed inline)*

## Phase 2: Context Storage & Injection

### Objective
Build the plumbing: load context from `.cercano/context.md` and inject it into all tool calls.

### Tasks
- [x] Task: Implement context loader — read `.cercano/context.md` from project root, cache in memory.
- [x] Task: Add `project_dir` optional parameter to all co-processor tools (summarize, extract, classify, explain).
- [x] Task: Implement context injection in MCP handlers — prepend loaded context to prompts when available.
- [x] Task: Implement session-level context cache on the MCP Server — load once per init, reuse across calls.
- [x] Task: Implement "not initialized" nudge — when a tool call includes a project dir but no `.cercano/context.md` exists, append a recommendation to the tool response suggesting `cercano_init`.
- [ ] Task: Red/Green TDD for all components.
- [-] Task: Conductor - User Manual Verification 'Context Storage & Injection' *(verified with Phase 4)*

## Phase 3: Repo Scanner & Context Builder

### Objective
Build the local intelligence that scans a repo and produces a useful context document.

### Tasks
- [x] Task: Implement file discovery — walk project dir, identify key files by name/extension (README, CLAUDE.md, .claude/memory/*, *.proto, *.h, config files, Makefile, go.mod, package.json, etc.).
- [x] Task: Implement size-aware file reading — read files up to a size limit, skip binaries and node_modules.
- [x] Task: Implement local model summarization — prompt template built, actual LLM call happens in cercano_init handler.
- [x] Task: Implement context assembly — Builder.BuildPrompt combines files + host context, Builder.WriteContext writes .cercano/context.md.
- [x] Task: Red/Green TDD for scanner and builder.
- [-] Task: Conductor - User Manual Verification 'Repo Scanner & Context Builder' *(verified with Phase 4)*

## Phase 4: cercano_init MCP Tool & Skill

### Objective
Ship the `cercano_init` tool and its Agent Skill definition.

### Tasks
- [x] Task: Implement `cercano_init` MCP tool — accepts `project_dir` (required) and `context` (optional host knowledge), runs scanner, builds context, loads it for the session.
- [x] Task: Write the `cercano_init` Agent Skill (SKILL.md) — explicit instructions that the host AI should only provide context it already has, and should provide `project_dir`.
- [x] Task: Handle re-init — overwrites existing context.md on re-run, invalidates cache.
- [-] Task: Add telemetry for init events *(deferred — low priority, existing tool telemetry covers basic tracking)*.
- [x] Task: Red/Green TDD.
- [x] Task: Update README.md with project context documentation.
- [x] Task: Conductor - User Manual Verification 'cercano_init MCP Tool & Skill' (Protocol in workflow.md)
