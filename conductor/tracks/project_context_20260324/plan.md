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
- [~] Task: Add `project_dir` optional parameter to all co-processor tools (summarize, extract, classify, explain).
- [~] Task: Implement context injection in MCP handlers — prepend loaded context to prompts when available.
- [~] Task: Implement session-level context cache on the MCP Server — load once per init, reuse across calls.
- [~] Task: Implement "not initialized" nudge — when a tool call includes a project dir but no `.cercano/context.md` exists, append a recommendation to the tool response suggesting `cercano_init`.
- [ ] Task: Red/Green TDD for all components.
- [ ] Task: Conductor - User Manual Verification 'Context Storage & Injection' (Protocol in workflow.md)

## Phase 3: Repo Scanner & Context Builder

### Objective
Build the local intelligence that scans a repo and produces a useful context document.

### Tasks
- [ ] Task: Implement file discovery — walk project dir, identify key files by name/extension (README, CLAUDE.md, .claude/memory/*, *.proto, *.h, config files, Makefile, go.mod, package.json, etc.).
- [ ] Task: Implement size-aware file reading — read files up to a size limit, skip binaries and node_modules.
- [ ] Task: Implement local model summarization — feed discovered files through cercano's local model to extract key domain knowledge (data structures, APIs, architecture, conventions).
- [ ] Task: Implement context assembly — combine local model output with any host-provided context into a structured context.md.
- [ ] Task: Red/Green TDD for scanner and builder.
- [ ] Task: Conductor - User Manual Verification 'Repo Scanner & Context Builder' (Protocol in workflow.md)

## Phase 4: cercano_init MCP Tool & Skill

### Objective
Ship the `cercano_init` tool and its Agent Skill definition.

### Tasks
- [ ] Task: Implement `cercano_init` MCP tool — accepts `project_dir` (required) and `context` (optional host knowledge), runs scanner, builds context, loads it for the session.
- [ ] Task: Write the `cercano_init` Agent Skill (SKILL.md) — explicit instructions that the host AI should only provide context it already has, and should provide `project_dir`.
- [ ] Task: Handle re-init — if `.cercano/context.md` already exists, offer to rebuild or append.
- [ ] Task: Add telemetry for init events (project scanned, context size, files processed).
- [ ] Task: Red/Green TDD.
- [ ] Task: Update README.md with project context documentation.
- [ ] Task: Conductor - User Manual Verification 'cercano_init MCP Tool & Skill' (Protocol in workflow.md)
