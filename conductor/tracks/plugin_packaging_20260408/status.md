# Plugin Packaging Track

**Status:** In progress — repos created, testing pending

## Repos

- [cercano-claude](https://github.com/bryancostanich/cercano-claude) — Claude Code plugin
- [cercano-gemini](https://github.com/bryancostanich/cercano-gemini) — Gemini CLI extension
- [cercano-codex](https://github.com/bryancostanich/cercano-codex) — Codex plugin

## Completed

- [x] Canonical skills in `plugins/skills/` with auto-routing triggers
- [x] Claude Code plugin repo — manifest, hooks, MCP config, 14 skills
- [x] Gemini CLI extension repo — manifest, GEMINI.md, commands, hooks, 14 skills
- [x] Codex plugin repo — manifest, MCP config, 14 skills
- [x] MCP progress notifications for research/deep_research/summarize
- [x] GitHub Action to sync skills to plugin repos

## Remaining

- [ ] Set PLUGIN_SYNC_TOKEN secret for GitHub Action
- [ ] Test Claude plugin installation and auto-routing
- [ ] Test Gemini extension installation and commands
- [ ] Test Codex plugin installation
- [ ] Submit to Claude marketplace (form at clau.de/plugin-directory-submission)
- [ ] Verify Gemini auto-listing via gemini-cli-extension topic
- [ ] Submit to Codex directory when available

## References

- Spec: `docs/superpowers/specs/2026-04-08-plugin-packaging-design.md`
- Plan: `docs/superpowers/plans/2026-04-08-plugin-packaging.md`
