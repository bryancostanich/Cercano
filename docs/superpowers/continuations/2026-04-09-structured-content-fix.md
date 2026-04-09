# Continuation: structuredContent Fix & Plugin Testing

## Context

Cercano MCP tool results stopped rendering in Claude Code terminal. The assistant receives results but the user sees nothing. This is a regression in Claude Code v2.0.21+ which prioritizes `structuredContent` for display — servers returning only `TextContent` get no output shown.

**Repo:** `/Users/bryancostanich/Git_Repos/bryan_costanich/Cercano`
**Branch:** `main`
**Filed issue:** anthropics/claude-code#45839

## What Was Done This Session

### structuredContent workaround implemented

Added a generic wrapper in `source/server/internal/mcp/server.go` that copies the first `TextContent` into `StructuredContent` on every tool result:

- `withStructuredContent()` — helper that sets `StructuredContent: map[string]string{"content": text}` from the first TextContent
- `wrapStructured[In, Out any]()` — generic wrapper for `ToolHandlerFor` handlers
- All 15 `AddTool()` registrations in `registerTools()` wrapped with `wrapStructured()`
- Build passes, MCP tests pass

### Binary rebuilt but not yet live-tested

The binary at `source/server/bin/cercano` has been rebuilt. Two old MCP server processes were still running (PIDs 63574, 75261). User needs to restart Claude Code to pick up the new binary.

## Immediate Next Step

1. **Verify the fix works** — call `cercano_stats` (or any cercano tool) and confirm the user can see the output in the terminal
2. If it works, commit the change
3. If it doesn't work, the `structuredContent` format may need adjustment — try `map[string]any{"type": "text", "text": tc.Text}` or a raw JSON string instead of the map

## Remaining Items (from prior continuation)

### Plugin testing (blocked on rendering fix)
- Re-install Claude Code plugin: `claude plugin install cercano@cercano-local`
- Test all 14 skills + MCP tools via plugin
- Test Gemini CLI extension (`cercano-gemini`)
- Test Codex plugin (`cercano-codex`)

### Marketplace submission
- Claude: Submit at `clau.de/plugin-directory-submission`
- Gemini: Auto-listed via `gemini-cli-extension` GitHub topic (check after 24h)
- Codex: Directory "coming soon"

### Housekeeping
- Rotate `PLUGIN_SYNC_TOKEN` (was exposed in conversation): `gh secret set PLUGIN_SYNC_TOKEN --repo bryancostanich/Cercano`

## Key Files

- MCP server (the fix): `source/server/internal/mcp/server.go`
- MCP tests: `source/server/internal/mcp/server_test.go`
- Prior continuation: `docs/superpowers/continuations/2026-04-09-plugin-testing.md`
- Plugin packaging spec: `docs/superpowers/specs/2026-04-08-plugin-packaging-design.md`
- Plugin packaging plan: `docs/superpowers/plans/2026-04-08-plugin-packaging.md`
- Conductor track: `conductor/tracks/plugin_packaging_20260408/`

## Important Rules

- **Never push without explicit approval**
- **Never file issues, PRs, or take public actions without explicit approval**
- **Never include "Claude" in commits**
