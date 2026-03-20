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

## Phase 2: Provider — Package Cercano Tools as Skills

### Objective
Create SKILL.md files for all Cercano MCP tools so they're discoverable by any Agent Skills-compatible agent.

### Tasks
- [~] Task: Create SKILL.md for `cercano_local`.
- [~] Task: Create SKILL.md for `cercano_summarize`.
- [~] Task: Create SKILL.md for `cercano_extract`.
- [~] Task: Create SKILL.md for `cercano_classify`.
- [~] Task: Create SKILL.md for `cercano_explain`.
- [~] Task: Create SKILL.md for `cercano_config`.
- [~] Task: Create SKILL.md for `cercano_models`.
- ~~Task: Create SKILL.md for `cercano_search`.~~ (tool does not exist yet)
- ~~Task: Create SKILL.md for `cercano_boilerplate`.~~ (tool does not exist yet)
- [ ] Task: Add `ListSkills` and `GetSkill` RPCs to `agent.proto`.
    - [ ] Red/Green TDD.
- [ ] Task: Implement gRPC skill service — serve built-in skill definitions from the server.
    - [ ] Red/Green TDD.
- [ ] Task: Add `cercano_skills` MCP tool wrapping the gRPC endpoints.
    - [ ] `action: "list"` → returns name + description for all skills.
    - [ ] `action: "get", name: "<skill>"` → returns full SKILL.md content.
    - [ ] Red/Green TDD.
- [ ] Task: End-to-end test — verify an external agent discovers and invokes a Cercano skill.
- [ ] Task: Conductor - User Manual Verification 'Provider Skills' (Protocol in workflow.md)

## Phase 3: Consumer — Skill Discovery & Activation

### Objective
Enable Cercano to discover external skills in a project and make them available for use.

### Tasks
- [ ] Task: Implement skill discovery — scan project for `.skills/` directories and parse SKILL.md files.
    - [ ] Red/Green TDD.
- [ ] Task: Implement skill registry — store discovered skills and their metadata.
    - [ ] Red/Green TDD.
- [ ] Task: Implement skill activation — register discovered skills as invocable tools.
    - [ ] Red/Green TDD.
- [ ] Task: Implement skill invocation — route calls to the skill's defined backend.
    - [ ] Red/Green TDD.
- [ ] Task: End-to-end test — add a third-party skill to a project, verify Cercano discovers and can invoke it.
- [ ] Task: Conductor - User Manual Verification 'Skill Discovery & Activation' (Protocol in workflow.md)

## Phase 4: Documentation & Polish

### Objective
Document Agent Skills support and ensure everything works together.

### Tasks
- [ ] Task: Update README.md with Agent Skills section.
    - [ ] Document provider capabilities (how to use Cercano's skills from other agents).
    - [ ] Document consumer capabilities (how to add skills to a project for Cercano to use).
- [ ] Task: Add a guide for creating custom SKILL.md files for Cercano.
- [ ] Task: Final integration test across provider + consumer.
- [ ] Task: Design and implement skill distribution/installation for end users.
    - [ ] How do skills get from a Homebrew (or binary) install into agent-discoverable directories?
    - [ ] Should `cercano` auto-detect installed agents (Claude Code, Cursor, Copilot, etc.) and install skills to their paths?
    - [ ] Should there be a `cercano skills install` command with `--agent` flag?
    - [ ] Symlinks vs copies — symlinks auto-update but may not work on all platforms.
    - [ ] Should first-run setup (`cercano init`) prompt the user?
    - [ ] Where do bundled skills live in the Homebrew prefix (e.g., `$(brew --prefix)/share/cercano/skills/`)?
- [ ] Task: Conductor - User Manual Verification 'Documentation & Polish' (Protocol in workflow.md)
