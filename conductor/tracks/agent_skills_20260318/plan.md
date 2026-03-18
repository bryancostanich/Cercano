# Track Plan: Agent Skills Integration

## Phase 1: Research & Design

### Objective
Understand the Agent Skills specification in depth, review how other agents implement it, and design Cercano's approach for both provider and consumer sides.

### Tasks
- [ ] Task: Deep-read the Agent Skills specification at agentskills.io.
    - [ ] Document the SKILL.md format, required fields, optional fields.
    - [ ] Identify how discovery works (file-based, registry, etc.).
    - [ ] Note any agent-specific extensions or variations.
- [ ] Task: Review how existing agents (Claude Code, Cursor, Copilot) discover and use skills.
    - [ ] What directory conventions do they scan?
    - [ ] How do they present discovered skills to the user/model?
- [ ] Task: Design Cercano's SKILL.md structure for provider skills.
    - [ ] Map each MCP tool to a SKILL.md file.
    - [ ] Define the directory layout (`.skills/` at repo root).
- [ ] Task: Design the consumer architecture — how Cercano discovers and activates external skills.
- [ ] Task: Conductor - User Manual Verification 'Research & Design' (Protocol in workflow.md)

## Phase 2: Provider — Package Cercano Tools as Skills

### Objective
Create SKILL.md files for all Cercano MCP tools so they're discoverable by any Agent Skills-compatible agent.

### Tasks
- [ ] Task: Create SKILL.md for `cercano_local`.
- [ ] Task: Create SKILL.md for `cercano_summarize`.
- [ ] Task: Create SKILL.md for `cercano_extract`.
- [ ] Task: Create SKILL.md for `cercano_search`.
- [ ] Task: Create SKILL.md for `cercano_classify`.
- [ ] Task: Create SKILL.md for `cercano_explain`.
- [ ] Task: Create SKILL.md for `cercano_boilerplate`.
- [ ] Task: Create SKILL.md for `cercano_config`.
- [ ] Task: Create SKILL.md for `cercano_models`.
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
- [ ] Task: Conductor - User Manual Verification 'Documentation & Polish' (Protocol in workflow.md)
