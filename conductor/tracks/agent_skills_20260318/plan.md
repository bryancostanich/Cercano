# Track Plan: Agent Skills Integration

## Phase 1: Research & Design [checkpoint: bca54a5]

### Objective
Understand the Agent Skills specification in depth, review how other agents implement it, and design Cercano's approach for both provider and consumer sides.

### Tasks
- [x] Task: Deep-read the Agent Skills specification at agentskills.io. `4f46af6`
    - [x] Document the SKILL.md format, required fields, optional fields.
    - [x] Identify how discovery works (file-based, registry, etc.).
    - [x] Note any agent-specific extensions or variations.
- [x] Task: Review how existing agents (Claude Code, Cursor, Copilot) discover and use skills. `4f46af6`
    - [x] What directory conventions do they scan?
    - [x] How do they present discovered skills to the user/model?
- [x] Task: Design Cercano's SKILL.md structure for provider skills. `4f46af6`
    - [x] Map each MCP tool to a SKILL.md file.
    - [x] Define the directory layout — target both `.agents/skills/` and `.claude/skills/`.
- [x] Task: Design the consumer architecture — how Cercano discovers and activates external skills. `d305c15`
- [x] Task: Conductor - User Manual Verification 'Research & Design' (Protocol in workflow.md)

## Phase 2: Provider — Package Cercano Tools as Skills [checkpoint: 7d23254]

### Objective
Create SKILL.md files for all Cercano MCP tools so they're discoverable by any Agent Skills-compatible agent.

### Tasks
- [x] Task: Create SKILL.md for `cercano_local`. `ac5614f`
- [x] Task: Create SKILL.md for `cercano_summarize`. `ac5614f`
- [x] Task: Create SKILL.md for `cercano_extract`. `ac5614f`
- [x] Task: Create SKILL.md for `cercano_classify`. `ac5614f`
- [x] Task: Create SKILL.md for `cercano_explain`. `ac5614f`
- [x] Task: Create SKILL.md for `cercano_config`. `ac5614f`
- [x] Task: Create SKILL.md for `cercano_models`. `ac5614f`
- ~~Task: Create SKILL.md for `cercano_search`.~~ (tool does not exist yet)
- ~~Task: Create SKILL.md for `cercano_boilerplate`.~~ (tool does not exist yet)
- [x] Task: Add `ListSkills` and `GetSkill` RPCs to `agent.proto`. `7f1a086`
    - [x] Red/Green TDD.
- [x] Task: Implement gRPC skill service — serve built-in skill definitions from the server. `7f1a086`
    - [x] Red/Green TDD.
- [x] Task: Add `cercano_skills` MCP tool wrapping the gRPC endpoints. `bc53c2a`
    - [x] `action: "list"` → returns name + description for all skills.
    - [x] `action: "get", name: "<skill>"` → returns full SKILL.md content.
    - [x] Red/Green TDD.
- [x] Task: End-to-end test — verify an external agent discovers and invokes a Cercano skill. `c10d960`
- [x] Task: Conductor - User Manual Verification 'Provider Skills' (Protocol in workflow.md)

## ~~Phase 3: Consumer — Skill Discovery & Activation~~ DEFERRED

> Moved to a future track. Needs real-world testing with a third-party tool that publishes Agent Skills before designing the consumer architecture. Key finding: SKILL.md files are prompt instructions, not tool registrations — the consumer side requires the agentic loop to read and follow skill instructions using existing tools.

## Phase 3: Documentation & Polish

### Objective
Document the provider-side Agent Skills support (SKILL.md files, `cercano_skills` MCP tool, skill distribution).

### Tasks
- [ ] Task: Update README.md with Agent Skills section.
    - [ ] Document provider capabilities (how to use Cercano's skills from other agents).
- [ ] Task: Add a guide for creating custom SKILL.md files for Cercano.
- [ ] Task: Design and implement skill distribution/installation for end users.
    - [ ] How do skills get from a Homebrew (or binary) install into agent-discoverable directories?
    - [ ] Should `cercano` auto-detect installed agents (Claude Code, Cursor, Copilot, etc.) and install skills to their paths?
    - [ ] Should there be a `cercano skills install` command with `--agent` flag?
    - [ ] Symlinks vs copies — symlinks auto-update but may not work on all platforms.
    - [ ] Should first-run setup (`cercano init`) prompt the user?
    - [ ] Where do bundled skills live in the Homebrew prefix (e.g., `$(brew --prefix)/share/cercano/skills/`)?
- [ ] Task: Conductor - User Manual Verification 'Documentation & Polish' (Protocol in workflow.md)
