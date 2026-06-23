# Docs Reorg Audit

> Audit of `docs/` ahead of a reorg toward "each major feature/plan gets its own
> folder, consistent buckets." Written 2026-06-23. Inventory + findings + two
> structure options. No files moved yet.

## Current inventory (52 md files)

| Bucket | Count | Contents |
|--------|-------|----------|
| `features/` | 21 flat `_spec.md` + `textarea/` folder | as-built specs |
| `plans/` | 13 flat | in-progress / not-built |
| `research/` | 1 | competitive_audit |
| `internal/` | 3 | ide_fixes, refactor_cleanup, test_fixtures |
| `agent/` | 1 | user-facing agent README |
| `scratch/v2upgrade/` | 4 | tui-charm-v2 + buffer-scrollbar (design+plan each) |
| `superpowers/{plans,specs}/` | 2 | streaming-markdown-render (plan+spec) |
| root loose | 7 | README, product, tech-stack, workflow, roadmap, agent-skills-guide, _migration_audit |
| `code_styleguides/` | 2 | general, go |

## Findings

### 1. Inconsistent granularity — the core problem
No rule for when a feature is a flat file vs a folder:
- 21 features are flat `features/<name>_spec.md`.
- `textarea` is the lone folder holding one file — `features/textarea/native_prompt_textarea_spec.md`.
- `native_tool_calling` is **3 flat files** that are one feature: `.md` (387 lines) + `_tasks.md` (3684!) + `_followups.md`.
- `living_recap` is **2 flat files**: `living_recap.md` (design) + `living_recap_plan.md` (plan, 1078 lines).

### 2. One initiative scattered across four buckets
The **standalone CLI / agent** work — the thing we're actively building — is spread across `plans/`, `scratch/`, `superpowers/`, and `features/textarea/`:

| File | Bucket |
|------|--------|
| cli.md | plans/ |
| native_tool_calling{,_tasks,_followups}.md | plans/ |
| living_recap{,_plan}.md | plans/ |
| native_prompt_textarea_spec.md | features/textarea/ |
| tui-charm-v2-{design,plan}.md | scratch/v2upgrade/ |
| buffer-scrollbar-{design,plan}.md | scratch/v2upgrade/ |
| streaming-markdown-render{,-design}.md | superpowers/{plans,specs}/ |

~12 CLI files, 4 buckets, 3 naming conventions. This is the strongest argument for the reorg.

### 3. Buckets mix three different organizing axes
- **By status** — `features/` (shipped) vs `plans/` (not built).
- **By doc-type** — `superpowers/specs/` vs `superpowers/plans/`.
- **By transience** — `scratch/`.

Pick one axis. Right now a doc's home depends on which tool created it and when, not what it's about.

### 4. Status-driven buckets force feature relocation
`features/` = shipped, `plans/` = not-built means a feature **moves folders when it ships**. That fights "each feature has its own folder" — the folder shouldn't relocate on a status flip. Already visible: `deep_research` is split — shipped spec in `features/deep_research_spec.md`, v2 redesign in `plans/deep_research_enhancement.md`.

### 5. Recurring design→plan→tasks triplet, stored inconsistently
Several features have the same internal shape but store it differently:
- living_recap: `living_recap.md` (design) + `living_recap_plan.md` (plan)
- native_tool_calling: `.md` (design) + `_tasks.md` + `_followups.md`
- streaming_markdown: `-design.md` (spec) + `.md` (plan)
- tui_charm_v2 / buffer_scrollbar: `-design.md` + `-plan.md`

A folder-per-feature with conventional filenames (`design.md`, `plan.md`, `tasks.md`) makes this uniform.

### 6. Related chains split apart
- `dispatch.md` is **superseded** by `native_tool_calling.md` (says so in its header) — belongs in an archive or as a note inside the NTC folder, not a peer plan.
- `validator_dispatch` → `test_fixtures` ("builds on validator-dispatch") → `dispatch` form one lineage, currently in `plans/` + `internal/`.
- `engine_agnosticism_spec` + `engine_bootstrap_spec` are one engine story.

## Two structure options

### Option A — status buckets, folder per feature (smaller change)
Keep `features/` / `plans/` / `research/` / `internal/` as status buckets. Inside each, every feature becomes a folder with `design.md` / `plan.md` / `tasks.md` / `spec.md` as needed.
- **Pro:** at-a-glance "what's shipped vs planned"; minimal conceptual change.
- **Con:** features still relocate on ship; the CLI initiative stays split across `plans/` and `features/`; doesn't fully fix finding #2.

### Option B — feature-centric, status as metadata (recommended)
One `features/` tree, **one folder per feature/initiative**, holding all its docs across its whole lifecycle. Status lives in a header field (most docs already have a `Status:` line), not in the path. Non-feature docs get clear evergreen buckets.

```
docs/
  README.md                     # index, grouped by status via the Status: field
  reference/                    # evergreen project-level (moved out of root)
    product.md  tech-stack.md  roadmap.md  workflow.md
    agent-skills-guide.md
    code-styleguides/{general,go}.md
  agent/README.md               # user-facing product doc (unchanged)
  features/
    cli/                        # the standalone CLI/agent initiative
      README.md                 # initiative overview (from cli.md)
      native-tool-calling/{design,tasks,followups}.md
      living-recap/{design,plan}.md
      textarea/spec.md
      tui-charm-v2/{design,plan}.md
      buffer-scrollbar/{design,plan}.md
      streaming-markdown/{design,plan}.md
    deep-research/{spec,enhancement-plan}.md
    engine/{agnosticism,bootstrap}.md
    distribution/  docker/  plugin-packaging/  semantic-search/
    savings-estimation/  validator-dispatch/  test-fixtures/
    adk-integration/  agent-skills/  ...  (one folder per remaining spec)
  research/competitive-audit/README.md
  archive/                      # superseded / historical
    dispatch.md  _migration_audit.md
```
- **Pro:** fixes #2 and #4; one home per feature for its whole life; consistent filenames; the CLI initiative is one navigable tree.
- **Con:** bigger move; "what's shipped" comes from a status field/index rather than the folder path (mitigated by a grouped README).

## Decisions & outcome (executed 2026-06-23)
1. **Option B (feature-centric)** — one folder per feature; status in headers, not paths.
2. **CLI grain** — one `features/cli/` initiative with a sub-folder per piece.
3. **Single-file features** — folder for every feature (uniform; `<name>/spec.md`).
4. **internal/ & agent/** — `internal/` kept for shipped maintenance records
   (`refactor-cleanup`, `ide-fixes`); `test-fixtures` promoted to `features/`;
   `agent/` kept as user-facing product docs. Evergreen project docs moved to
   `reference/`. Superseded `dispatch.md` + audits moved to `archive/`.

Migration done via `git mv` (history preserved) except the 4 `tui-charm-v2` /
`buffer-scrollbar` docs, which were in gitignored `scratch/` and were moved with
plain `mv` — they now sit in tracked `features/cli/` as **new untracked files**
(decide whether to `git add` them). All cross-references updated except the
historical task-script bodies in `native-tool-calling/tasks.md` (Tasks 32–34),
left as-is since they document pre-reorg doc surgery.
