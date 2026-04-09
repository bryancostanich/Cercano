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
- Local marketplace for testing at `/tmp/cercano-marketplace` (symlinks to cercano-claude)

## What's Left

### 1. Install and test Claude Code plugin

A local marketplace `cercano-local` was added. Install with:

```bash
claude plugin install cercano@cercano-local
```

Then restart Claude Code and test:
- "research how MCP progress notifications work" — should auto-route to `cercano_research`
- "fetch https://ollama.com/blog" — should auto-route to `cercano_fetch`
- Verify progress messages appear for research tools
- Verify all 14 `cercano_*` tools are available

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
