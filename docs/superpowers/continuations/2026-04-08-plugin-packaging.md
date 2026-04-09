# Continuation: Cercano Plugin/Extension Packaging

## Context

Cercano is a local-first AI dev tool (Go, Ollama-powered) that runs as an MCP server. We just shipped v0.9.0 with a major deep research redesign (three-tier incremental research, sidecar state, progress tracker, fetcher improvements).

**Repo:** `/Users/bryancostanich/Git_Repos/bryan_costanich/Cercano`
**Branch:** `main` (v0.9.0 tagged and released)

## What We're Doing

Package Cercano as a plugin/extension for each major AI coding tool that has a proper plugin system. The goal is marketplace distribution and native integration (not just raw MCP).

## Target Platforms

| Tool | Plugin Model | What to build |
|---|---|---|
| **Claude Code** | Plugins (MCP + skills + hooks + sub-agents), Anthropic marketplace | Claude plugin package |
| **Gemini CLI** | Extensions (MCP + playbooks + commands), Google marketplace | Gemini extension package |
| **GitHub Copilot** | Copilot Extensions (chat-based `@agent`), GitHub Marketplace | Copilot extension (different model — chat-based, not MCP) |

Cursor and Windsurf already work via raw MCP — no packaging needed.
Codex CLI has no plugin system — skip.

## Why Plugins > Raw MCP

Two problems with raw MCP that plugins solve:
1. **Adoption** — Claude won't use Cercano tools unless explicitly told to, even with CLAUDE.md instructions. Plugins with bundled playbooks/skills get tighter integration.
2. **Feedback** — MCP progress notifications are broken in Claude Code. Plugins can use hooks or sub-agents for better UX.

## What We Know

- Claude plugins launched Jan 2026. Bundle skills + MCP servers + hooks + sub-agents. Marketplace exists.
- Gemini extensions bundle MCP servers + playbooks + commands. Marketplace with Google + partner extensions.
- Copilot extensions are chat-based (`@agent` invocation). Different from MCP — they're API endpoints that receive chat context. GitHub Marketplace.
- Our research findings on this topic were thin (fetcher issues). Need to go directly to each platform's developer docs.

## Suggested Approach

1. **Research each platform's developer docs** — read the actual plugin/extension authoring guides
2. **Start with Claude plugin** — we know the ecosystem best, Cercano already has skills defined
3. **Then Gemini extension** — similar MCP-based model
4. **Then Copilot extension** — different model, may need an adapter layer

## Key Files

- Cercano MCP server: `source/server/cmd/cercano/` (entry point)
- MCP tool handlers: `source/server/internal/mcp/server.go`
- Existing skills: `.agents/skills/` and `.claude/skills/`
- Existing MCP config: `.mcp.json`
- Homebrew formula: `../homebrew-cercano/Formula/cercano.rb`

## Specs and Plans

- Design spec: `docs/superpowers/specs/2026-04-08-deep-research-v2-design.md` (for reference on how we work)
- Implementation plan: `docs/superpowers/plans/2026-04-08-deep-research-v2.md`

## Also On the Radar

- **Blog post + socialization** — write after plugin work so we can announce everything together
- **Issue #4** — multi-pass research decomposition (filed, future enhancement)
- **Fetcher headless browser support** — JS-heavy sites still return minimal content (future track)

## Temporary Config State

Check memory file `project_cercano_testing.md` — there may be temp config changes to revert:
- `~/.config/cercano/config.yaml` may be pointing at localhost instead of remote Mac Studio
- Worktree at `../Cercano-research-v2` can be cleaned up

## Conductor

This project uses a track-based development workflow in `conductor/`. New work should get a track in `conductor/tracks/`.
