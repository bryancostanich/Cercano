# Track Plan: Deep Research Enhancement

## Phase 0: Model Check & Auto-Switch

### Tasks
- [x] Task: Define research-capable model list and code-only model detection.
- [x] Task: Pre-check model before deep research runs. Returns immediately with suggestion if wrong model.
- [x] Task: Per-request model override via `use_model` parameter (proto + full pipeline).
- [ ] ~~Task: Add `research_model` config field.~~ Removed — we check what's being used, not bake specific models.

## Phase 1: Multi-Pass Analysis Pipeline

### Tasks
- [x] Task: Pass 1 — Fact Extraction with example-driven prompt.
- [x] Task: Pass 2 — Relevance Analysis with cross-finding context and scoring guide.
- [x] Task: Pass 3 — Quality Gate with re-prompt on failure.
- [x] Task: Wire three passes into `AnalyzeFinding`.

## Phase 2: Cross-Finding Context

### Tasks
- [x] Task: `BuildCrossContext` — 1-line summaries, capped at 15.
- [x] Task: Cross-context passed to AnalyzeRelevance.
- [x] Task: `AnalyzeAll` accumulates context as it processes.

## Phase 3: Quality Gate with Re-Prompting

- [x] Implemented as part of Phase 1 (Pass 3).

## Phase 4: Depth Over Breadth

- [x] Task: Updated `DefaultConfig` — survey: 3/source, thorough: 6/source.
- [x] Task: Updated planner prompt with specific query examples.

## Phase 5: Example-Driven Prompts

- [x] Task: Examples in fact extraction, relevance analysis, and planner prompts.

## Phase 6: Progress Updates

- [x] Task: `ProgressWriter` — stderr + `status.md` in output dir.
- [x] Task: `AnalyzeAllWithProgress` — per-finding progress messages.
- [x] Task: Pipeline wired with progress at every phase.

## Phase 7: Phased Execution

- [x] Task: Pipeline supports `phase` parameter: plan, search, analyze, synthesize.
- [x] Task: Each phase returns results and suggests next step.
- [x] Task: State preserved via checkpoints between calls.
- [x] Task: MCP handler wires `phase` parameter through.

## Phase 8: Parallel Content Prefetching

- [x] Task: `PrefetchContent` — fetches all URLs concurrently.
- [x] Task: `SearchAndPrefetch` — overlaps search + fetch across sources.
- [x] Task: Content map checkpointed for analyze phase.
- [x] Task: `AnalyzeAllWithPrefetch` reads from prefetched map.

## Phase 9: Validation & Polish

### Status: NOT STARTED — pick up here

### Tasks
- [ ] Task: Push pending commits and test phased flow end-to-end with parallelized fetching.
- [ ] Task: Run timed before/after comparison (pre-parallelization vs post).
- [ ] Task: Address remaining issues:
    - [ ] Relevance scores still cluster at 4/5 — needs calibration.
    - [ ] DDG searcher returning more results than max_results limit.
    - [ ] Irrelevant results from broad DDG queries (hiring threads, incident pages).
    - [ ] Consider filtering thin content (< N chars) before analysis.
- [ ] Task: Update SKILL.md with: phased execution, model check, use_model parameter.
- [ ] Task: Update track plan for deep_research_20260326 (parent track) with completion status.
- [ ] Task: Conductor - User Manual Verification 'Integration & Validation' (Protocol in workflow.md)

## Unpushed Commits

There are commits not yet pushed to origin:
- `feat(research): Pre-check model before running deep research, not after`
- `conductor(plan): Add formal before/after validation for research enhancement`
- `feat(research): Per-request model override and pre-run model check flow`
- `feat(research): Phased execution — plan, search, analyze, synthesize as separate steps`
- `perf(research): Parallel content prefetching overlapped with search`
- `conductor(plan): Update deep research enhancement track with completion status`
