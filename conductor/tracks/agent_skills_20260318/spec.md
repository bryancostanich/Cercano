# Track Specification: Agent Skills Integration

## 1. Job Title
Adopt the Agent Skills open standard — package Cercano's tools as discoverable skills and enable Cercano to consume external skills.

## 2. Overview
[Agent Skills](https://agentskills.io) is a portable, file-based format (SKILL.md) for giving agents discoverable capabilities. It's supported by 25+ agent products including Claude Code, Cursor, Copilot, and Codex.

This track has two sides:
1. **Provider** — Package Cercano's local co-processor tools (summarize, extract, search, classify, explain, boilerplate) as SKILL.md files so that any Agent Skills-compatible agent can discover and use them without manual MCP configuration.
2. **Consumer** — Enable Cercano to discover and activate community/enterprise skills, extending its own capabilities.

### Dependencies
- **Local Co-Processor Tools track** must be substantially complete (at least the MVP tools) before the Provider side can be meaningfully built.
- **Competitive Audit track** findings may inform which skills to prioritize and how other agents handle skill discovery.

**What changes:** SKILL.md files added for each Cercano tool, a skill discovery/loader mechanism added to Cercano, and documentation updated.

**What does NOT change:** The underlying MCP tools and gRPC RPCs. Skills are a packaging/discovery layer on top of existing tools.

## 3. Architecture Decision

### Provider Side
```
Repository root/
├── .skills/
│   ├── cercano-summarize/
│   │   └── SKILL.md        # Describes the summarize capability
│   ├── cercano-extract/
│   │   └── SKILL.md
│   ├── cercano-search/
│   │   └── SKILL.md
│   └── ...
```

Each SKILL.md describes:
- What the skill does (human-readable + machine-readable)
- How to invoke it (MCP tool name, parameters)
- Prerequisites (Cercano server running, Ollama available)
- Example usage

### Consumer Side
```
Cercano Server
    │
    ├── Skill Discovery
    │   ├── Scan project for .skills/ directories
    │   ├── Scan community skill registries
    │   └── Load skill definitions
    │
    └── Skill Activation
        ├── Register discovered skills as available tools
        └── Route skill invocations to the appropriate backend
```

Key decisions:
- **SKILL.md is the interface contract** — It's the single source of truth for what a skill does and how to use it.
- **MCP is the transport** — Skills are invoked via MCP tool calls. The SKILL.md just makes them discoverable.
- **Provider first** — Packaging Cercano's tools as skills is higher value and lower risk than building a full consumer/loader.

## 4. Requirements

### 4.1 Provider: SKILL.md Files
- Create a SKILL.md file for each Cercano MCP tool.
- Follow the Agent Skills specification format.
- Include clear descriptions, parameter documentation, and usage examples.
- Test that Agent Skills-compatible agents can discover and invoke the skills.

### 4.2 Consumer: Skill Discovery
- Scan the current project directory for `.skills/` directories.
- Parse SKILL.md files and register discovered skills.
- Support activating/deactivating skills at runtime.

### 4.3 Consumer: Skill Invocation
- Route skill invocations through the appropriate backend (MCP, HTTP, CLI, etc. as defined in the SKILL.md).
- Handle errors gracefully when a skill's backend is unavailable.

### 4.4 Documentation
- Document how to create SKILL.md files for Cercano tools.
- Document how to use Cercano as a skill consumer.
- Update README with Agent Skills support information.

## 5. Acceptance Criteria
- [ ] Each Cercano MCP tool has a corresponding SKILL.md file.
- [ ] At least one external agent (Claude Code, Cursor) can discover Cercano's skills via the standard mechanism.
- [ ] Cercano can discover and list skills from a project's `.skills/` directory.
- [ ] Cercano can invoke a discovered skill.
- [ ] Documentation is complete and accurate.

## 6. Out of Scope
- Building the underlying MCP tools — that's the Local Co-Processor Tools track.
- Competitive research — that's the Competitive Audit track.
- Hosting a skill registry or marketplace.
- Skill versioning or dependency management (v1 keeps it simple).
