# Docs Migration Audit

Audit of all planning material before reorg into `/docs/features/*_spec.md` (shipped)
and `/docs/plans/*` (still planning). Status = best-guess from checkbox completion in
`plan.md` + git activity + README. **Checkbox counts are partly stale** (e.g. `cli`
shows 17 done but recent commits are heavy CLI work) — borderline calls flagged.

## Sources found

| Source | Count | Nature |
|---|---|---|
| `conductor/archive/` | 19 | Shipped tracks (archived) |
| `conductor/tracks/` | 14 | Active tracks (mixed) |
| `docs/superpowers/` | 8 | plans/specs/continuations |
| `.superpowers/brainstorm/` | ~15 html | Scratch UI mockups |

## conductor/archive/ → shipped (→ features/spec)

All 19 are shipped. Most become a feature spec; a few are maintenance, not features.

| Track | Becomes |
|---|---|
| adk_integration | feature spec |
| agent_skills | feature spec |
| auto_server_launch | feature spec |
| cloud_integration | feature spec |
| configurable_local_model | feature spec |
| document_tool | feature spec |
| generalize_agent | feature spec |
| ide_enhancements | feature spec |
| ide_fixes | maintenance (flag) |
| local_ai_mvp | feature spec |
| local_coprocessor_tools | feature spec |
| mcp_server | feature spec |
| project_context | feature spec |
| refactor_cleanup | maintenance (flag) |
| remote_inference | feature spec |
| token_streaming | feature spec |
| update_check | feature spec |
| usage_telemetry | feature spec |
| web_research | feature spec |

## conductor/tracks/ → classified

| Track | x / todo | Bucket |
|---|---|---|
| deep_research_20260326 | 58 / 2 | BUILT → spec |
| engine_bootstrap_20260325 | 40 / 5 | BUILT → spec |
| distribution_20260317 | 61 / 8 | BUILT? → spec (flag) |
| cli | 17 / 284 | PARTIAL → plan |
| engine_agnosticism_20260317 | 24 / 29 | PARTIAL → plan |
| plugin_packaging_20260408 | repos done | PARTIAL → plan |
| deep_research_enhancement_20260329 | 25 / 11 | PARTIAL → plan (flag) |
| dispatch_20260530 | 0 / 66 | PLANNING → plan |
| docker_20260320 | 0 / 18 | PLANNING → plan |
| savings_estimation_20260326 | 0 / 38 | PLANNING → plan |
| semantic_search_20260318 | 0 / 18 | PLANNING → plan |
| validator_dispatch_20260529 | 0 / 67 | PLANNING → plan |
| test_fixtures_20260601 | 0 / 45 | infra, not feature (flag) |
| competitive_audit_20260318 | 0 / 22 | research doc, not feature (flag) |

## docs/superpowers/ → overlaps to resolve

| File | Overlaps |
|---|---|
| plans/deep-research-v2 | conductor deep_research_enhancement |
| specs/deep-research-v2-design | same |
| plans/plugin-packaging | conductor plugin_packaging |
| specs/plugin-packaging-design | same |
| continuations/* (4) | session handoffs, not specs |

`.superpowers/brainstorm/*.html` = CLI UI mockups (banner/palette/layout). Scratch, not docs.

## Open decisions (need confirmation before moving)

1. **Move vs copy** — delete `conductor/` originals after migration, or copy and keep
   conductor as the working/active dir?
2. **Non-feature tracks** — competitive_audit (research), test_fixtures (infra),
   refactor_cleanup + ide_fixes (maintenance): own bucket, or skip?
3. **Superpowers duplicates** — merge into the conductor-derived doc, or keep separate?
4. **Naming** — strip date suffix from filenames? plans as flat file or per-feature dir?
5. **Borderline built/partial** — confirm distribution (built?), engine_agnosticism
   (partial), deep_research_enhancement (header says NOT STARTED but 25 boxes checked).
