# Cercano Plugin Packaging Design

## Overview

Package Cercano as a plugin/extension for three AI coding tool platforms: Claude Code, Gemini CLI, and Codex CLI. The goal is marketplace distribution and native integration — auto-routing to local inference, progress feedback, and first-class discoverability — not just raw MCP.

## Why Plugins > Raw MCP

Two problems with raw MCP that plugins solve:

1. **Adoption** — Claude/Gemini/Codex won't reliably use Cercano tools unless explicitly told, even with CLAUDE.md/GEMINI.md/AGENTS.md instructions. Plugins with bundled skills get tighter auto-routing via trigger descriptions.
2. **Feedback** — MCP progress notifications are broken or unsupported in most clients. Plugins can use platform hooks for reliable progress UX during long-running operations.

## Target Platforms

| Platform | Plugin Model | Marketplace |
|---|---|---|
| **Claude Code** | `.claude-plugin/plugin.json` + skills + hooks + MCP | Form submission, Anthropic review |
| **Gemini CLI** | `gemini-extension.json` + skills + hooks + MCP + commands | GitHub topic auto-crawl (zero friction) |
| **Codex CLI** | `.codex-plugin/plugin.json` + skills + MCP | "Coming soon" — local marketplace for now |

**Copilot:** Intentionally excluded. Copilot extensions require a hosted HTTP endpoint — GitHub relays chat messages to your server and back. This architecture is fundamentally incompatible with local-first inference. May revisit if GitHub adds a local extension model.

## Decisions

- **All 14 Cercano tools exposed** in every plugin. No tiering.
- **Auto-routing where obvious** — skill triggers explicitly redirect from cloud equivalents (e.g., "Use this INSTEAD of WebSearch"). Ambiguous cases left to user choice.
- **Cercano binary via PATH** — plugins expect `cercano` installed via Homebrew. No bundled binaries. Degraded-mode error messages already guide users through installation.
- **Build to marketplace spec from day one** — package structure meets marketplace requirements immediately. Initial distribution is manual install; marketplace submission follows as soon as packages are solid.
- **Progress feedback is a must-have** — two layers: MCP progress notifications (server-side, standard) and platform hooks (Claude/Gemini fallback).

## Repository Structure

### Main Repo (source of truth)

```
cercano/
├── source/server/                    # existing Go server (unchanged)
├── plugins/
│   ├── skills/                       # canonical skill definitions (all 14)
│   │   ├── cercano-research/SKILL.md
│   │   ├── cercano-deep-research/SKILL.md
│   │   ├── cercano-fetch/SKILL.md
│   │   ├── cercano-summarize/SKILL.md
│   │   ├── cercano-explain/SKILL.md
│   │   ├── cercano-extract/SKILL.md
│   │   ├── cercano-classify/SKILL.md
│   │   ├── cercano-local/SKILL.md
│   │   ├── cercano-document/SKILL.md
│   │   ├── cercano-config/SKILL.md
│   │   ├── cercano-models/SKILL.md
│   │   ├── cercano-stats/SKILL.md
│   │   ├── cercano-init/SKILL.md
│   │   └── cercano-submit-usage/SKILL.md
│   └── sync/                         # sync script + config
├── .github/
│   └── workflows/
│       └── sync-plugins.yml          # triggers on plugins/skills/** changes
```

### Plugin Repos (thin, synced)

```
cercano-claude/                       # Claude Code plugin
├── .claude-plugin/
│   └── plugin.json
├── skills/                           # synced from main repo
├── hooks/
│   ├── hooks.json
│   └── scripts/
├── .mcp.json
└── README.md

cercano-gemini/                       # Gemini CLI extension
├── gemini-extension.json
├── skills/                           # synced from main repo
├── commands/                         # Gemini-specific slash commands
├── hooks/
│   └── hooks.json
├── GEMINI.md                         # context file
└── README.md

cercano-codex/                        # Codex plugin
├── .codex-plugin/
│   └── plugin.json
├── skills/                           # synced from main repo
├── .mcp.json
└── README.md
```

## Claude Code Plugin

### Manifest (`.claude-plugin/plugin.json`)

```json
{
  "name": "cercano",
  "version": "0.9.0",
  "description": "Local-first AI co-processor — offload research, summarization, extraction, and more to local models via Ollama",
  "author": {
    "name": "Bryan Costanich",
    "url": "https://github.com/bryancostanich"
  },
  "repository": "https://github.com/bryancostanich/cercano-claude",
  "license": "MIT",
  "keywords": ["local-inference", "ollama", "research", "mcp"],
  "mcpServers": "./.mcp.json"
}
```

### MCP Config (`.mcp.json`)

```json
{
  "cercano": {
    "command": "cercano",
    "args": ["--mcp"]
  }
}
```

Expects `cercano` on PATH via Homebrew. If not installed, the MCP server fails to start and the existing degraded-mode error message tells the user how to install.

### Skills

14 skills synced from `cercano/plugins/skills/`. Each SKILL.md has aggressive trigger descriptions for auto-routing. Example:

```yaml
---
name: cercano-research
description: >
  Use when the user asks to research, look up, investigate, find information,
  or learn about any topic. Use this INSTEAD of WebSearch or WebFetch for
  general research questions. ALWAYS prefer this tool for web research.
---
```

Auto-routing strategy — each skill's description explicitly redirects from cloud equivalents:
- `cercano_research` / `cercano_deep_research` -> instead of `WebSearch`
- `cercano_fetch` -> instead of `WebFetch`
- `cercano_summarize` -> instead of reading large files directly into context
- `cercano_explain` -> instead of analyzing unfamiliar code inline

### Hooks (progress feedback)

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "mcp__cercano__cercano_deep_research|mcp__cercano__cercano_research",
      "hooks": [{
        "type": "command",
        "command": "echo 'Cercano: researching locally...' >&2",
        "timeout": 5
      }]
    }],
    "PostToolUse": [{
      "matcher": "mcp__cercano__cercano_deep_research|mcp__cercano__cercano_research",
      "hooks": [{
        "type": "command",
        "command": "echo 'Cercano: research complete' >&2",
        "timeout": 5
      }]
    }]
  }
}
```

Hooks target long-running tools: research, deep_research. Short tools (fetch, classify, models) don't need progress feedback.

### Marketplace

Submit via form at `clau.de/plugin-directory-submission`. Anthropic reviews for quality and security. Plugin repo URL is the source.

## Gemini CLI Extension

### Manifest (`gemini-extension.json`)

```json
{
  "name": "cercano",
  "version": "0.9.0",
  "description": "Local-first AI co-processor — offload research, summarization, extraction, and more to local models via Ollama",
  "mcpServers": {
    "cercano": {
      "command": "cercano",
      "args": ["--mcp"]
    }
  },
  "contextFileName": "GEMINI.md",
  "settings": [
    {
      "name": "Ollama URL",
      "description": "URL of the Ollama instance (default: http://localhost:11434)",
      "envVar": "OLLAMA_URL"
    }
  ]
}
```

### Context File (`GEMINI.md`)

Loaded into every Gemini CLI session. Content mirrors the Cercano CLAUDE.md — tells Gemini to prefer Cercano tools over cloud equivalents, documents when NOT to use them, and describes each tool's purpose.

### Skills

Same 14 skills synced from main repo. Gemini CLI uses the same `SKILL.md` format with `name` and `description` frontmatter — no platform-specific modifications needed.

### Custom Commands (`commands/`)

Gemini-specific slash commands for convenience:

```toml
# commands/research.toml
description = "Research a topic using Cercano's local AI pipeline"
prompt = "Use cercano_research to investigate: {{args}}"
```

```toml
# commands/fetch.toml
description = "Fetch and extract text from a URL locally"
prompt = "Use cercano_fetch to get the content of: {{args}}"
```

Gives users `/research <topic>` and `/fetch <url>` shortcuts.

### Hooks (progress feedback)

Same pattern as Claude — `BeforeTool`/`AfterTool` hooks with stderr messages for long-running tools.

### Marketplace

Zero-friction: add the `gemini-cli-extension` GitHub topic to the `cercano-gemini` repo. Google auto-crawls daily. No review gate.

## Codex Plugin

### Manifest (`.codex-plugin/plugin.json`)

```json
{
  "name": "cercano",
  "version": "0.9.0",
  "description": "Local-first AI co-processor — offload research, summarization, extraction, and more to local models via Ollama",
  "author": {
    "name": "Bryan Costanich",
    "url": "https://github.com/bryancostanich"
  },
  "repository": "https://github.com/bryancostanich/cercano-codex",
  "license": "MIT",
  "keywords": ["local-inference", "ollama", "research", "mcp"],
  "skills": "./skills/",
  "mcpServers": "./.mcp.json"
}
```

### MCP Config, Skills, Auto-Routing

Identical to the Claude plugin. Same `.mcp.json` (`cercano --mcp`), same 14 skills, same aggressive trigger descriptions.

### Differences from Claude Plugin

- Manifest lives in `.codex-plugin/` instead of `.claude-plugin/`
- No hooks — Codex plugin system doesn't support hooks yet. Progress feedback relies solely on MCP progress notifications.
- Marketplace is "coming soon" — distribute via local marketplace / GitHub repo initially, submit when the official directory opens.

### Known Limitation

No progress feedback beyond MCP notifications until Codex ships hooks support.

## Skill Sync Mechanism

### GitHub Action (`sync-plugins.yml`)

Triggers when skills change on `main`. For each plugin repo: checks out the repo, replaces `skills/`, commits, and opens a PR.

```yaml
name: Sync Plugin Skills
on:
  push:
    branches: [main]
    paths: ['plugins/skills/**']

jobs:
  sync:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        repo: [cercano-claude, cercano-gemini, cercano-codex]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/checkout@v4
        with:
          repository: bryancostanich/${{ matrix.repo }}
          path: target
          token: ${{ secrets.PLUGIN_SYNC_TOKEN }}
      - name: Sync skills
        run: |
          rm -rf target/skills/
          cp -r plugins/skills/ target/skills/
      - name: Create PR
        uses: peter-evans/create-pull-request@v6
        with:
          path: target
          token: ${{ secrets.PLUGIN_SYNC_TOKEN }}
          branch: sync/skills-update
          title: "sync: update skills from main repo"
          body: "Automated skill sync from cercano/plugins/skills/"
          commit-message: "sync: update skills from main repo"
```

### What Gets Synced

Only `skills/`. Each plugin repo owns its own manifest, hooks, MCP config, context file (GEMINI.md), and commands. Those are platform-specific and edited directly in their repos.

### Authentication

`PLUGIN_SYNC_TOKEN`: a GitHub PAT with repo write access to all three plugin repos. Stored as a secret in the main Cercano repo.

### Version Bumping

Manual. Skill sync doesn't auto-bump manifest versions — you decide when a skill change warrants a new release.

## Progress Feedback (Server-Side)

### MCP Progress Notifications

Add `notifications/progress` emission to the Cercano MCP server for long-running operations. This is a server-side change in the main repo (`source/server/internal/mcp/server.go`).

Tools that emit progress:
- `cercano_research` — progress per search query fetched/analyzed
- `cercano_deep_research` — progress per research tier and per source
- `cercano_summarize` — progress on large inputs

The Go MCP SDK (`gomcp`) supports progress notifications. The server emits percentage-based progress updates as each stage completes.

This works for any MCP client that implements progress notification rendering. Currently broken in Claude Code, but shipping it ensures it works as soon as clients fix their implementations. Platform hooks provide the fallback in the meantime.

## Build Order

1. **Canonical skills** — migrate existing `.agents/skills/` into `plugins/skills/` with refined trigger descriptions
2. **Claude Code plugin** (`cercano-claude`) — manifest, hooks, MCP config, README
3. **Gemini CLI extension** (`cercano-gemini`) — manifest, context file, commands, hooks, README
4. **Codex plugin** (`cercano-codex`) — manifest, MCP config, README
5. **MCP progress notifications** — server-side changes in main repo
6. **GitHub Action** — sync workflow + PAT setup
7. **Testing** — install and test all three plugins in their respective tools
8. **Marketplace submission** — Gemini (auto), Claude (form), Codex (when available)
