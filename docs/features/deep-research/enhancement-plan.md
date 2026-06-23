# Deep Research Enhancement

## Overview / Goal

Improve the analytical quality of `cercano_deep_research` output to produce genuinely useful research — specific, insightful, cross-referenced — rather than structurally correct but shallow summaries. Then (v2) redesign the pipeline around three research tiers with incremental deepening, a progress tracker, and a sidecar state file, while fixing the bugs surfaced in validation.

### The problem (v1)

Current output is structurally sound (correct sections, star ratings, all fields) but analytically shallow: summaries read like book reports; each finding is analyzed in isolation with no cross-referencing; relevance scores are inflated (everything 4–5 stars); vague output is accepted with no quality check.

### Root causes
- **Single-pass, overloaded prompts** — one call asks the model to do 7 things at once; small models degrade.
- **No cross-finding context** — finding #15 analyzed with zero knowledge of #1–14.
- **No quality gate** — content-free sentences ("relevant to the competitive landscape") are recorded as-is.
- **Breadth over depth** — many sources skimmed shallowly instead of fewer read deeply.
- **Model mismatch** — `qwen3-coder` is a coding model: great at structured output, shallow on analytical reasoning.

## Design / Approach

### v1 fixes (enhancement track)

1. **Multi-pass analysis pipeline** — replace the single call with three focused passes: (1) fact extraction, (2) relevance analysis (with cross-finding context + scoring guide), (3) critique/quality gate with one re-prompt on failure.
2. **Cross-finding context window** — when analyzing finding N, include 1-line summaries of prior findings so the model can draw connections.
3. **Quality gate with re-prompting** — PASS/FAIL check for specific facts/metrics/methods; on FAIL re-prompt with the critique. Max 1 retry.
4. **Depth over breadth** — fewer sources, deeper per-finding analysis; larger content truncation; 3–4 model calls per finding instead of 1.
5. **Model-appropriate, example-driven prompts** — show BAD vs GOOD output in each prompt; small models learn better from examples.

Constraints: must work with `qwen3-coder`; latency and token usage increase (~3x per finding, acceptable); backward compatible (same MCP interface and output format).

### v2 redesign — three-tier incremental research

Three tiers with speed/depth trade-offs; `standard` replaces `thorough` as the new default. All tiers keep the full 3-pass analysis (facts + relevance + quality gate).

| Parameter | Survey | Standard (default) | Deep |
|---|---|---|---|
| Sources | 2-3 | 3-4 | 4-5 |
| Queries/source | 1-2 | 2-3 | 3 |
| Results/query | 3 | 4 | 6 |
| Page truncate | 8K | 10K | 12K |
| Analysis truncate | 10K | 12K | 15K |
| Reference chasing | none | 1-hop, max 15 / 3 per finding | 1-hop, max 50 / 5 per finding |
| Est. time | ~2 min | ~5-8 min | ~15+ min |

**State persistence via sidecar.** Replace the ephemeral checkpoint system with a persistent `research_state.json` in the output dir (version, depth, topic/intent, plan, search_results, content_cache, findings, sections, progress, timestamps). Written after every meaningful step for crash recovery; `progress.phase = "complete"` on success.

**Incremental deepening.** When called with an `output_dir` containing existing state: load + validate; if requested depth ≤ existing, return existing with a note; else expand the plan (planner picks complementary sources, avoids duplicates), search only new queries, fetch only uncached URLs, analyze only new publications (cross-context from existing findings), selectively re-score (Pass 2 only) existing findings at 2–4, chase references if the new tier allows, re-synthesize, overwrite reports + sidecar.

**Suggested next action.** After survey/standard, append a "Next Steps" markdown section and a structured `suggested_next` field in the MCP response metadata (deepen → next tier on same output_dir). Deep is terminal — suggests follow-up queries from gap analysis instead.

**Progress tracker.** `ProgressTracker` struct with rolling-average ETA; writes granular `status.md` + one-line stderr updates + sidecar `progress` field. Crash recovery resumes from the last completed step.

**Bug fixes:** (6.1) relevance score calibration — explicit 1–5 anchors + "use the full range, most should be 2–4, reserve 5"; (6.2) DDG result overflow — hard-cap results to `MaxPrimaryResults` per query Go-side after dedup; (6.3) irrelevant broad queries — narrower planner prompts + post-search title keyword-overlap filter (`FilterByKeywordOverlap`); (6.4) thin content — skip analysis on extracted content < 500 chars.

**Tech stack:** Go, research pipeline in `source/server/internal/research/`.

### Validation result (v1 before/after)

Test topic "local AI inference for developer tools." The model switch (qwen3-coder → qwen2.5:72b) produced the largest quality gain; multi-pass pipeline, better search queries, and example-driven prompts added incrementally. Measured: findings 50 → 21 (fewer/deeper), irrelevant ~30% → ~14%, vague summaries ~20% → ~10%, summaries with specific numbers ~30% → ~60%, cross-finding connections none → limited. Still-open: score discrimination collapsed from "everything 4–5" to "everything 4."

## Status

### v1 enhancement track (mostly complete)

**Phase 0 — Model check & auto-switch**
- [x] Define research-capable model list + code-only model detection
- [x] Pre-check model before deep research runs (returns suggestion if wrong model)
- [x] Per-request model override via `use_model` (proto + full pipeline)
- [x] ~~Add `research_model` config field~~ — removed (check what's used, don't bake specific models)

**Phase 1 — Multi-pass analysis pipeline**
- [x] Pass 1 fact extraction (example-driven prompt)
- [x] Pass 2 relevance analysis (cross-finding context + scoring guide)
- [x] Pass 3 quality gate (re-prompt on failure)
- [x] Wire three passes into `AnalyzeFinding`

**Phase 2 — Cross-finding context**
- [x] `BuildCrossContext` — 1-line summaries, capped at 15
- [x] Cross-context passed to `AnalyzeRelevance`
- [x] `AnalyzeAll` accumulates context as it processes

**Phase 3 — Quality gate with re-prompting**
- [x] Implemented as part of Phase 1 (Pass 3)

**Phase 4 — Depth over breadth**
- [x] `DefaultConfig` updated (survey 3/source, thorough 6/source)
- [x] Planner prompt updated with specific query examples

**Phase 5 — Example-driven prompts**
- [x] Examples in fact extraction, relevance analysis, planner prompts

**Phase 6 — Progress updates**
- [x] `ProgressWriter` — stderr + `status.md`
- [x] `AnalyzeAllWithProgress` — per-finding messages
- [x] Pipeline wired with progress at every phase

**Phase 7 — Phased execution**
- [x] Pipeline supports `phase` (plan/search/analyze/synthesize)
- [x] Each phase returns results + suggests next step
- [x] State preserved via checkpoints between calls
- [x] MCP handler wires `phase` through

**Phase 8 — Parallel content prefetching**
- [x] `PrefetchContent` — concurrent URL fetch
- [x] `SearchAndPrefetch` — overlap search + fetch
- [x] Content map checkpointed for analyze phase
- [x] `AnalyzeAllWithPrefetch` reads from prefetched map

**Phase 9 — Validation & polish (NOT STARTED — pick up here)**
- [ ] Push pending commits; test phased flow end-to-end with parallelized fetching
- [ ] Run timed before/after comparison (pre- vs post-parallelization)
- [ ] Address remaining issues:
    - [ ] Relevance scores cluster at 4/5 — needs calibration
    - [ ] DDG searcher returns more than `max_results`
    - [ ] Irrelevant results from broad DDG queries (hiring threads, incident pages)
    - [ ] Filter thin content (< N chars) before analysis
- [ ] Update SKILL.md (phased execution, model check, `use_model`)
- [ ] Update parent track `deep_research_20260326` with completion status
- [ ] Conductor user manual verification 'Integration & Validation'

### v2 redesign track (planned — not started)

Builds on the unpushed v1 commits, which must land first.

**Task 1 — Three-tier config** (survey/standard/deep, remove `thorough`, `DepthOrder`, default → standard)
- [ ] Tests for three-tier config + `DepthOrder`
- [ ] Update `DeepResearchConfig` / `DefaultConfig` in `types.go`
- [ ] Default depth → `standard` in `pipeline.go`

**Task 2 — Sidecar state file** (`research_state.json` replacing ephemeral checkpoints)
- [ ] Add sidecar types (`ResearchState`, `ProgressState`, `CurrentStateVersion`)
- [ ] Tests: save/load, exists, `IsInProgress`
- [ ] Rewrite `checkpoint.go` → `sidecar.go` (`NewSidecar`, `NewState`)

**Task 3 — Progress tracker** (ETA + granular `status.md`, replaces `ProgressWriter`)
- [ ] Tests: ETA, status file, Done deletes status, ProgressState
- [ ] Rewrite `progress.go` (`NewProgressTracker`, backward-compatible `Update`/`Done`)
- [ ] Pipeline uses `NewProgressTracker`

**Task 4 — Bug fix: relevance score calibration**
- [ ] Score anchors + "use full range" instruction in `AnalyzeRelevance` prompt

**Task 5 — Bug fix: DDG result cap**
- [ ] Test `SearchSource` caps to `MaxPrimaryResults`
- [ ] Hard cap after dedup in `SearchSource`

**Task 6 — Bug fix: thin content filter**
- [ ] Test `AnalyzeAllWithPrefetch` skips < 500-char content
- [ ] Add `minContentChars` skip in `AnalyzeAllWithPrefetch`

**Task 7 — Bug fix: better query generation**
- [ ] Tests for `FilterByKeywordOverlap`
- [ ] Implement `FilterByKeywordOverlap` in `search.go`
- [ ] Apply title filtering in `SearchAndPrefetch` / `SearchAllSources`
- [ ] Verify planner good/bad query examples present (already added in v1)

**Task 8 — Pipeline integration: sidecar + incremental deepening**
- [ ] Tests: incremental deepening skips existing work; same depth returns existing
- [ ] Refactor `pipeline.go` `Run` for sidecar load + deepening (replace checkpoint usage)
- [ ] `PlanExpansion` in `planner.go` (complementary sources)
- [ ] `ReAnalyzeMiddleFindings` in `analyze.go` (re-score 2–4 findings)

**Task 9 — Suggested next action**
- [ ] Tests for `FormatNextSteps` (survey suggests standard; deep suggests nothing)
- [ ] `FormatNextSteps` in `report.go` + append to synthesis
- [ ] `SuggestedNext` on `PhaseResult`; populate on survey/standard completion
- [ ] Wire `suggested_next` into MCP response metadata

**Task 10 — MCP handler updates**
- [ ] Update `DeepResearchRequest` depth description (survey/standard/deep, default standard)
- [ ] Update `cercano-deep-research` SKILL.md (three-tier depth, incremental usage)

**Task 11 — Push prior enhancement commits**
- [ ] Verify the 6 unpushed enhancement commits present
- [ ] Ask user for push approval (do NOT push without explicit approval)

### Structured-content output fix (related, cross-cuts plugin packaging)

Claude Code v2.0.21+ prioritizes `structuredContent` for display; servers returning only `TextContent` show no visible output — so research results (and all tool output) reach the assistant but not the user. Filed anthropics/claude-code#45839.
- [x] `wrapStructured[In, Out any]()` + `withStructuredContent()` in `source/server/internal/mcp/server.go`, applied to all 15 `AddTool()` registrations (build + MCP tests pass)
- [ ] Live-test attempt 2 format `map[string]any{"type":"text","text":tc.Text}` after Claude Code restart (old MCP procs still running)
- [ ] If still rendering as JSON garbage, try raw string, then nested content-block array, then check go-sdk examples
- [ ] Commit once a clean-rendering format is confirmed

## Open Questions / Notes

### Remaining quality issues (from validation)
1. **Score clustering** — moved from everything-4-5 to everything-4; needs stronger calibration in the relevance prompt (addressed by v2 Task 4).
2. **Thin findings** — GitHub topic pages / archived repos produce thin content; filter by length (v2 Task 6).
3. **Weak cross-references** — cross-context is passed in but the model rarely draws explicit connections; may need a dedicated cross-reference pass.
4. **Reference chasing produced 0 results** in the after-run — quality gate may be too aggressive with citation extraction.

### v2 non-goals (deferred to plugin track)
MCP notification/streaming (broken in Claude Code), `cercano_research_status` poll tool, push-based progress to host agent, Claude plugin / Gemini extension packaging, adoption/discoverability improvements.

### Prior work that must land first (v2 foundation)
Six unpushed commits from the enhancement track: model pre-check, per-request model override, phased execution, parallel prefetching, validation results, track status update. Sidecar/incremental design builds on these. (Per project rules: never push without explicit approval.)
