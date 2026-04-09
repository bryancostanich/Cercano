# Continuation: structuredContent Fix — Iteration 2

## Context

Cercano MCP tool results stopped rendering in Claude Code terminal. Claude Code v2.0.21+ prioritizes `structuredContent` for display — servers returning only `TextContent` get no visible output.

**Repo:** `/Users/bryancostanich/Git_Repos/bryan_costanich/Cercano`
**Branch:** `main`
**Filed issue:** anthropics/claude-code#45839

## What Was Done

### Attempt 1: `map[string]string{"content": tc.Text}` — PARTIALLY WORKED
- Output appeared but was wrapped in a JSON block with no line breaks or wrapping
- Unreadable mess — the map got serialized as `{"content":"...huge text..."}` and Claude Code displayed that JSON blob

### Attempt 2: `map[string]any{"type": "text", "text": tc.Text}` — NOT YET TESTED
- Changed the structuredContent format to match MCP text content block structure
- Binary rebuilt at `source/server/bin/cercano`
- **Two old MCP processes still running** (PIDs 63574, 89836) — need Claude Code restart to pick up new binary

### Implementation details
- `withStructuredContent()` in `source/server/internal/mcp/server.go` (~line 254) — sets `StructuredContent` from first TextContent
- `wrapStructured[In, Out any]()` — generic wrapper applied to all 15 `AddTool()` registrations in `registerTools()`
- Build passes, tests pass

## Immediate Next Step

1. **Verify the fix works** — call `cercano_stats` (or any cercano tool) and confirm the user can see clean, readable output in the terminal
2. If it renders cleanly: commit the change
3. If it still looks like JSON garbage, try these alternatives in order:
   a. Set `StructuredContent` to the raw string: `result.StructuredContent = tc.Text`
   b. Try wrapping as `map[string]any{"content": [map[string]any{"type": "text", "text": tc.Text}]}`
   c. Look at how other Go MCP servers set structuredContent (search GitHub for `StructuredContent` in go-sdk examples)

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

- MCP server (the fix): `source/server/internal/mcp/server.go` (~line 254)
- MCP tests: `source/server/internal/mcp/server_test.go`
- Prior continuations: `docs/superpowers/continuations/2026-04-09-structured-content-fix.md`, `2026-04-09-plugin-testing.md`
- Plugin packaging spec: `docs/superpowers/specs/2026-04-08-plugin-packaging-design.md`

## Important Rules

- **Never push without explicit approval**
- **Never file issues, PRs, or take public actions without explicit approval**
- **Never include "Claude" in commits**
