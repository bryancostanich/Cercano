# Continuation: Plugin Testing & Marketplace Submission

## Context

Cercano plugin packaging is nearly complete. Three plugin repos have been created and pushed to GitHub, MCP progress notifications added to the server, and a GitHub Action syncs skills across repos. We're now at the testing and marketplace submission phase.

**Repo:** `/Users/bryancostanich/Git_Repos/bryan_costanich/Cercano`
**Branch:** `main`

## What's Done

- Canonical skills in `plugins/skills/` with aggressive auto-routing triggers (14 skills)
- `cercano-claude` repo — https://github.com/bryancostanich/cercano-claude
- `cercano-gemini` repo — https://github.com/bryancostanich/cercano-gemini (with `gemini-cli-extension` topic)
- `cercano-codex` repo — https://github.com/bryancostanich/cercano-codex
- MCP progress notifications for research, deep_research, summarize
- GitHub Action `sync-plugins.yml` (triggers on `plugins/skills/**` changes)
- `PLUGIN_SYNC_TOKEN` secret set (needs rotation — was pasted in conversation)
- Conductor track at `conductor/tracks/plugin_packaging_20260408/`

## Current Investigation: Plugin MCP Result Rendering Bug

### Problem
MCP tool results from plugin-provided servers are **not displayed to the user** in the Claude Code terminal. The assistant receives the results (and can summarize them), but the user sees only the tool call label — no output.

### What We Tested
1. Installed `cercano@cercano-local` plugin — all 14 skills loaded, all MCP tools functional
2. Called `cercano_research`, `cercano_fetch`, `cercano_classify`, `cercano_summarize`, `cercano_explain`, `cercano_stats` — all returned correct results to the assistant
3. **User saw zero MCP tool output** for any of these calls
4. Cercano's MCP server correctly returns `TextContent` only (no `structuredContent`) — verified in source code at `source/server/internal/mcp/server.go`

### Current State
- Plugin has been **uninstalled** (`claude plugin uninstall cercano@cercano-local`)
- Cercano MCP server is registered as a **user-scoped MCP server** (pre-existing, via `claude mcp add --scope user`)
- The local marketplace at `/tmp/cercano-marketplace` still exists but plugin is not installed
- Need to test whether the **user-scoped MCP server** renders results (it should — this is how it worked before the plugin)

### Next Step
1. **Restart Claude Code** and test `cercano_stats` (or any cercano tool) — results should render now via the user-scoped MCP server
2. If results render: **confirmed bug** — plugin-provided MCP results don't display, user-scoped ones do
3. If results still don't render: issue is elsewhere (Claude Code version regression?)
4. Once confirmed, file issue at `anthropics/claude-code`

### Related GitHub Issue
- https://github.com/anthropics/claude-code/issues/9962 — "Undocumented breaking change in MCP tool output display" (structuredContent prioritization in v2.0.21+, but our issue is different — we only return TextContent)

## What's Left (After Bug Investigation)

### 1. Re-install and test Claude Code plugin
Once the rendering bug is understood, re-install the plugin and verify the workaround or fix:
```bash
claude plugin install cercano@cercano-local
```

### 2. Test Gemini CLI extension

```bash
gemini extensions install https://github.com/bryancostanich/cercano-gemini
# or for local testing:
gemini extensions link /Users/bryancostanich/Git_Repos/bryan_costanich/cercano-gemini
```

Test:
- `/research transformer architecture` — custom command
- `/fetch https://ollama.com/blog` — custom command
- Natural language research question — auto-routing via skills
- Verify GEMINI.md context loads

### 3. Test Codex plugin

```bash
mkdir -p ~/.codex/plugins
cp -r /Users/bryancostanich/Git_Repos/bryan_costanich/cercano-codex ~/.codex/plugins/cercano
```

Test:
- "research how MCP progress notifications work" — auto-routing
- Verify tools available

### 4. Marketplace submission

- **Claude:** Submit at `clau.de/plugin-directory-submission`
- **Gemini:** Already auto-listed via `gemini-cli-extension` GitHub topic (check after 24h)
- **Codex:** Directory "coming soon" — no action needed yet

### 5. Rotate PLUGIN_SYNC_TOKEN

The PAT was exposed in a conversation. Regenerate at https://github.com/settings/personal-access-tokens and update:

```bash
gh secret set PLUGIN_SYNC_TOKEN --repo bryancostanich/Cercano
```

## Key Files

- Spec: `docs/superpowers/specs/2026-04-08-plugin-packaging-design.md`
- Plan: `docs/superpowers/plans/2026-04-08-plugin-packaging.md`
- Track status: `conductor/tracks/plugin_packaging_20260408/status.md`
- Canonical skills: `plugins/skills/`
- Sync workflow: `.github/workflows/sync-plugins.yml`
- MCP progress code: `source/server/internal/mcp/server.go` (search for `notifyProgress`)

## Conductor

Track: `conductor/tracks/plugin_packaging_20260408/`
