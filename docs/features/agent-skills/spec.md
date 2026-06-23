# Agent Skills Integration

## Overview

Cercano adopted the [Agent Skills](https://agentskills.io) open standard — a portable, file-based `SKILL.md` format for giving agents discoverable capabilities, supported by 30+ agent products (Claude Code, Cursor, Copilot, VS Code, Codex, Gemini CLI, and others). The shipped work is the **provider** side: Cercano's local co-processor MCP tools are packaged as `SKILL.md` files plus a dynamic, MCP-served skill catalog, so any Agent Skills-compatible agent can discover and invoke them without manual MCP configuration. Skills are a packaging/discovery layer on top of the existing MCP tools and gRPC RPCs — those underlying tools were not changed.

## Design / Architecture

### SKILL.md format (the interface contract)

Each skill is a directory containing a `SKILL.md` (YAML frontmatter + Markdown body) plus optional `scripts/`, `references/`, and `assets/`. Required frontmatter: `name` (max 64 chars, lowercase/hyphens, must match the parent directory name) and `description` (max 1024 chars — the primary signal for agent matching, so keyword quality is critical). Bodies are kept under ~500 lines / ~5000 tokens.

Discovery follows three-tier progressive disclosure: catalog (name + description loaded at session start), instructions (full body loaded when a task matches), resources (supporting files loaded only when referenced).

### Two discovery paths

1. **Dynamic (MCP)** — Agents connected to Cercano call the `cercano_skills` MCP tool to fetch the catalog; no file installation needed.
2. **Static (filesystem)** — Agents scan `.agents/skills/<name>/SKILL.md` (cross-client standard) or `.claude/skills/<name>/SKILL.md` (Claude Code). Skills are maintained in `.agents/skills/` as source of truth.

```
Cercano Server
    ├── ListSkills() gRPC  → catalog tier (name + description)
    └── GetSkill(name) gRPC → instructions tier (full SKILL.md)
          ▲
          │ wrapped by
    cercano_skills MCP tool
      ├── action:"list"        → catalog of all skills
      └── action:"get", name   → full skill definition
```

This mirrors Cercano's standard pattern (MCP wraps gRPC). `ListSkills` and `GetSkill` RPCs were added to `agent.proto` and serve the built-in skill definitions directly from the server.

## Key Behaviors / Capabilities

- Seven published skills, one per existing MCP tool: `cercano-local`, `cercano-models`, `cercano-config`, `cercano-summarize`, `cercano-extract`, `cercano-classify`, `cercano-explain`. Descriptions lead with what the tool does, embed task-matching keywords, emphasize "local / private / without cloud," and note the prerequisite that the Cercano server (and Ollama) must be running.
- External agents (e.g. Claude Code, Cursor) can discover and invoke Cercano's skills via the standard mechanism (verified end-to-end).
- `cercano_skills` MCP tool serves the catalog and full definitions dynamically over the existing MCP connection.
- Documentation: README Agent Skills section (lists the 7 skills, discovery, manual install) and `docs/agent-skills-guide.md` (frontmatter format, directory layout, conventions, worked example wrapping a Cercano MCP tool).

## Notable Decisions / Constraints

- **Provider first.** Packaging Cercano's tools as skills was judged higher value and lower risk than building a full consumer/loader.
- **Consumer side deferred.** Key finding driving the deferral: `SKILL.md` files are prompt instructions, not tool registrations — a consumer requires the agentic loop to read and follow skill instructions using existing tools. This needs real-world testing against a third-party publisher before designing the architecture.
- Two SKILL.md files originally planned (`cercano-search`, `cercano-boilerplate`) were dropped because those MCP tools do not exist yet.
- Out of scope: hosting a skill registry/marketplace, and skill versioning or dependency management (v1 kept simple).
- Gotchas baked into the skills: `name` must equal the directory name; unquoted colons in YAML descriptions break parsing; Claude Code frontmatter extensions (e.g. `argument-hint`, `context: fork`) are not portable to other agents; project-level skills from untrusted repos can inject instructions.
