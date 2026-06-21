# Cercano Docs

Project documentation, organized by status. Migrated from the former
`conductor/` track workflow and superpowers planning docs (2026-06-21).

## `features/` — shipped features (as-built specs)

Specs describing features that have shipped. One `<name>_spec.md` per feature.

- adk_integration, agent_skills, auto_server_launch, cloud_integration,
  configurable_local_model, deep_research, distribution, document_tool,
  engine_agnosticism, engine_bootstrap, generalize_agent, ide_enhancements,
  local_ai_mvp, local_coprocessor_tools, mcp_server, project_context,
  remote_inference, token_streaming, update_check, usage_telemetry, web_research

> `distribution` and a few others carry a short "Remaining / not-yet-done"
> section for the small sub-items still open.

## `plans/` — in-progress / not-yet-built

Planning docs with intent, design, and remaining task lists.

- **cli** — stand-alone terminal agent harness (plan drafted, not implemented)
- **plugin_packaging** — Claude/Gemini/Codex plugin repos (in progress; testing pending)
- **deep_research_enhancement** — deep-research v2 three-tier redesign + structured-output fix
- **dispatch**, **validator_dispatch** — agent/validator dispatch (TDD plans, not started)
- **docker** — containerization (not started)
- **savings_estimation** — token-savings estimation (not started)
- **semantic_search** — embedding-based codebase search (not started)

## `research/` — research deliverables

- **competitive_audit** — agent-features landscape audit. NOTE: scoped only, the
  per-agent investigation was never run; matrices are scaffolded with TBD cells.

## `internal/` — infra / maintenance

- **test_fixtures** — test-fixture infrastructure (planning)
- **refactor_cleanup** — server/client restructure (shipped)
- **ide_fixes** — VS Code extension bug fixes (shipped)

## Project-level reference

- `product.md` — product vision / concept
- `tech-stack.md` — technology stack
- `workflow.md` — development workflow / process
- `roadmap.md` — master project plan (track links are historical; tracks now live
  under `features/` and `plans/`)
- `code_styleguides/` — `general.md`, `go.md`

## Other

- `agent-skills-guide.md` — guide to writing SKILL.md files
- `_migration_audit.md` — record of this docs reorg
