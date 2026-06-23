# Cercano Docs

Project documentation, organized **by feature** — each major feature or initiative
gets its own folder under `features/`, holding all of its docs (design, plan, tasks,
spec) across its whole lifecycle. **Status lives in each doc's header**, not in the
folder path, so a feature's folder never moves when it ships. Reorg history is in
`archive/` (`_migration_audit.md`, `_reorg_audit.md`).

## `features/` — one folder per feature/initiative

Each folder holds the docs that feature needs, using conventional filenames:
`spec.md` (as-built), `design.md` (approved design), `plan.md` / `tasks.md`
(implementation), `followups.md`. Check the `Status:` header inside each doc.

### `features/cli/` — the standalone CLI / agent initiative
The active build. One sub-folder per piece:

- `README.md` — initiative overview (the CLI track plan)
- `native-tool-calling/` — design, tasks, followups
- `living-recap/` — design, plan
- `textarea/` — native prompt textarea spec
- `tui-charm-v2/` — Charm v2 migration design + plan
- `buffer-scrollbar/` — viewport scrollbar design + plan
- `streaming-markdown/` — streaming markdown render design + plan

### Other features (shipped & planned)
`adk-integration`, `agent-skills`, `auto-server-launch`, `cloud-integration`,
`configurable-local-model`, `deep-research` (spec + enhancement-plan),
`distribution`, `docker`, `document-tool`, `engine` (agnosticism + bootstrap),
`generalize-agent`, `ide-enhancements`, `local-ai-mvp`, `local-coprocessor-tools`,
`mcp-server`, `plugin-packaging`, `project-context`, `remote-inference`,
`savings-estimation`, `semantic-search`, `test-fixtures`, `token-streaming`,
`update-check`, `usage-telemetry`, `validator-dispatch`, `web-research`

## `research/` — research deliverables

- `competitive-audit.md` — agent-features landscape audit. NOTE: scoped only; the
  per-agent investigation was never run; matrices are scaffolded with TBD cells.

## `internal/` — infra / maintenance records

- `refactor-cleanup.md` — server/client restructure (shipped)
- `ide-fixes.md` — VS Code extension bug fixes (shipped)

## `reference/` — evergreen project-level

- `product.md` — product vision / concept
- `tech-stack.md` — technology stack
- `workflow.md` — development workflow / process
- `roadmap.md` — master project plan (links into `features/`)
- `agent-skills-guide.md` — guide to writing SKILL.md files
- `code-styleguides/` — `general.md`, `go.md`

## `agent/` — user-facing agent documentation

- `README.md` — how to set up and use the standalone Cercano agent

## `archive/` — superseded / historical

- `dispatch.md` — raw local-LLM dispatch design, superseded by `features/cli/native-tool-calling/`
- `_migration_audit.md` — record of the first docs reorg (conductor → docs)
- `_reorg_audit.md` — record of this feature-centric reorg
