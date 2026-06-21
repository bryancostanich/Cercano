# Plugin Packaging

## Overview / Goal

Package Cercano as a plugin/extension for AI coding tool platforms — Claude Code, Gemini CLI, and Codex CLI — for marketplace distribution and native integration (auto-routing to local inference, progress feedback, first-class discoverability), not just raw MCP.

### Why plugins > raw MCP

1. **Adoption** — Claude/Gemini/Codex won't reliably use Cercano tools unless explicitly told, even with CLAUDE.md/GEMINI.md/AGENTS.md instructions. Plugins with bundled skills get tighter auto-routing via trigger descriptions.
2. **Feedback** — MCP progress notifications are broken or unsupported in most clients today. Plugins can use platform hooks for reliable progress UX on long-running operations.

### Target platforms

| Platform | Plugin model | Marketplace |
|---|---|---|
| Claude Code | `.claude-plugin/plugin.json` + skills + hooks + MCP | Form submission, Anthropic review |
| Gemini CLI | `gemini-extension.json` + skills + hooks + MCP + commands | GitHub topic auto-crawl |
| Codex CLI | `.codex-plugin/plugin.json` + skills + MCP | "Coming soon" — local marketplace for now |

**Copilot:** intentionally excluded — Copilot extensions require a hosted HTTP endpoint (GitHub relays chat to your server), fundamentally incompatible with local-first inference. Cursor / Windsurf already work via raw MCP — no packaging needed.

## Design / Approach

### Decisions

- All 14 Cercano tools exposed in every plugin. No tiering.
- Auto-routing where obvious — skill triggers explicitly redirect from cloud equivalents (`cercano_research`/`deep_research` → instead of WebSearch; `cercano_fetch` → instead of WebFetch; `cercano_summarize` → instead of reading large files; `cercano_explain` → instead of inline analysis). Ambiguous cases left to user.
- Cercano binary via PATH — plugins expect `cercano` installed via Homebrew. No bundled binaries; degraded-mode error messages guide installation.
- Build to marketplace spec from day one; initial distribution manual, marketplace submission follows.
- Progress feedback is a must-have — two layers: MCP progress notifications (server-side, standard) + platform hooks (Claude/Gemini fallback).

### Repository structure

Main repo is source of truth: canonical skills in `plugins/skills/` (all 14), plus `.github/workflows/sync-plugins.yml`. Three thin synced plugin repos:
- `cercano-claude` — `.claude-plugin/plugin.json`, `skills/` (synced), `hooks/hooks.json`, `.mcp.json`, README
- `cercano-gemini` — `gemini-extension.json`, `skills/` (synced), `commands/` (slash commands), `hooks/hooks.json`, `GEMINI.md`, README
- `cercano-codex` — `.codex-plugin/plugin.json`, `skills/` (synced), `.mcp.json`, README

### Per-platform specifics

- **Claude:** manifest references `./.mcp.json` (`cercano --mcp`); `PreToolUse`/`PostToolUse` hooks echo progress to stderr for `cercano_deep_research`/`research`/`summarize`; submit via `clau.de/plugin-directory-submission`.
- **Gemini:** `gemini-extension.json` with inline `mcpServers`, `contextFileName: GEMINI.md`, `OLLAMA_URL` setting; `BeforeTool`/`AfterTool` hooks (note: single-underscore tool names `mcp_cercano_cercano_research` vs Claude's double-underscore); `commands/research.toml` and `commands/fetch.toml`; `GEMINI.md` mirrors Cercano CLAUDE.md; zero-friction marketplace via `gemini-cli-extension` GitHub topic (auto-crawled daily).
- **Codex:** manifest in `.codex-plugin/`; identical MCP config + 14 skills + triggers to Claude; **no hooks** (Codex doesn't support them yet — progress relies solely on MCP notifications); local marketplace install until official directory opens.

### Skill sync mechanism

`sync-plugins.yml` triggers on `plugins/skills/**` push to main; matrix over the three repos checks out each, replaces `skills/`, opens a PR. Only `skills/` is synced — each repo owns its manifest, hooks, MCP config, GEMINI.md, commands. Auth via `PLUGIN_SYNC_TOKEN` PAT (repo write to all three). Version bumping is manual.

### Server-side progress notifications

Add `notifications/progress` emission to the MCP server (`source/server/internal/mcp/server.go`) via the Go MCP SDK (`req.Session.NotifyProgress()`, `gomcp` v0.7.0). Tools that emit: `cercano_research` (per query), `cercano_deep_research` (per tier/source), `cercano_summarize` (on large inputs). Works for any MCP client that renders progress; currently broken in Claude Code but ships so it works when clients fix their side. Platform hooks are the fallback.

**Tech stack:** Go (MCP server progress), YAML/JSON/TOML (manifests), GitHub Actions (sync), Bash (scripts).

## Status

**In progress — repos created, testing pending.**

### Repos created
- [cercano-claude](https://github.com/bryancostanich/cercano-claude)
- [cercano-gemini](https://github.com/bryancostanich/cercano-gemini) (with `gemini-cli-extension` topic)
- [cercano-codex](https://github.com/bryancostanich/cercano-codex)

### Completed
- [x] Canonical skills in `plugins/skills/` with auto-routing triggers (14 skills)
- [x] Claude Code plugin repo — manifest, hooks, MCP config, 14 skills
- [x] Gemini CLI extension repo — manifest, GEMINI.md, commands, hooks, 14 skills
- [x] Codex plugin repo — manifest, MCP config, 14 skills
- [x] MCP progress notifications for research / deep_research / summarize
- [x] GitHub Action `sync-plugins.yml` to sync skills to plugin repos
- [x] `PLUGIN_SYNC_TOKEN` secret set (needs rotation — was pasted in conversation)
- [x] Conductor track at `conductor/tracks/plugin_packaging_20260408/`

### Remaining
- [ ] Re-set / rotate `PLUGIN_SYNC_TOKEN` (PAT was exposed in conversation) — regenerate fine-grained PAT (Contents r/w + Pull requests r/w on all three repos), `gh secret set PLUGIN_SYNC_TOKEN --repo bryancostanich/Cercano`
- [ ] Resolve the plugin MCP result-rendering bug (see Open Questions) before plugin testing
- [ ] Test Claude plugin installation and auto-routing (`claude plugin install cercano@cercano-local`; verify 14 skills + tools, progress on stderr, auto-route on "research…" / "fetch …")
- [ ] Test Gemini extension installation and commands (`gemini extensions install …` / `link`; `/research`, `/fetch`, natural-language auto-routing, GEMINI.md loads)
- [ ] Test Codex plugin installation (`cp -r … ~/.codex/plugins/cercano`; auto-routing, tools available)
- [ ] Fix any platform-specific issues found (tool-name underscore formats, hook event names, trigger refinements)
- [ ] Submit to Claude marketplace (`clau.de/plugin-directory-submission`)
- [ ] Verify Gemini auto-listing via `gemini-cli-extension` topic (check after 24h)
- [ ] Submit to Codex directory when available (monitor developers.openai.com/codex/plugins)

## Open Questions / Notes

### Plugin MCP result-rendering bug (blocks plugin testing)

MCP tool results from plugin-provided servers are **not displayed to the user** in the Claude Code terminal — the assistant receives results (and can summarize them) but the user sees only the tool-call label. Confirmed: Cercano's MCP server correctly returns `TextContent` only (no `structuredContent`). This is a Claude Code v2.0.21+ regression that prioritizes `structuredContent` for display — servers returning only `TextContent` get no visible output. Related upstream: anthropics/claude-code#9962; filed anthropics/claude-code#45839.

**Workaround in progress** (`source/server/internal/mcp/server.go`): a generic `wrapStructured[In, Out any]()` wrapper + `withStructuredContent()` helper that copies the first `TextContent` into `StructuredContent` on every result (all 15 `AddTool()` registrations wrapped; build + MCP tests pass). Iteration history:
- Attempt 1 `map[string]string{"content": tc.Text}` — partially worked but rendered as an unreadable single-line JSON blob.
- Attempt 2 `map[string]any{"type": "text", "text": tc.Text}` — **not yet live-tested** (old MCP processes still running; needs Claude Code restart).
- If still garbage, try in order: raw string `result.StructuredContent = tc.Text`; `map[string]any{"content": [{"type":"text","text":tc.Text}]}`; check go-sdk examples for how `StructuredContent` should be set.

Open diagnostic: confirm whether **user-scoped** MCP servers render results while **plugin-provided** ones do not (would isolate the bug to plugin delivery). Once confirmed and the workaround verified, commit the fix, then re-install and test the plugin.

### Other notes
- Blog post + socialization to follow plugin work (announce everything together).
- Issue #4 — multi-pass research decomposition (filed, future enhancement).
- Fetcher headless-browser support — JS-heavy sites return minimal content (future track).
- Possible temp config to revert: `~/.config/cercano/config.yaml` may point at localhost instead of remote Mac Studio; worktree at `../Cercano-research-v2` can be cleaned up.

### Superpowers sources folded into this doc
- `docs/superpowers/specs/2026-04-08-plugin-packaging-design.md` (design)
- `docs/superpowers/plans/2026-04-08-plugin-packaging.md` (8-task plan)
- `docs/superpowers/continuations/2026-04-08-plugin-packaging.md` (origin / target-platform notes)
- `docs/superpowers/continuations/2026-04-09-plugin-testing.md` (testing + rendering-bug investigation)
- The structuredContent rendering work originates in the two `structured-content-fix` continuations; it is also tracked in the Deep Research Enhancement plan since it affects research output formatting.
