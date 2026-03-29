# Track Plan: Deep Research Enhancement

## Phase 0: Model Check & Auto-Switch

### Objective
Detect when the active model is a poor fit for research tasks and prompt the user before wasting time.

### Tasks
- [x] Task: Define research-capable model list and code-only model detection.
    - [x] `IsCodeOnlyModel`, `SuggestResearchModel`, `CheckResearchModel`.
    - [x] Red/Green TDD: TestIsCodeOnlyModel, TestSuggestResearchModel, TestCheckResearchModel_SuggestsSwitch, TestCheckResearchModel_NoNoteWhenModelIsFine.
- [x] Task: Pre-check model before deep research runs.
    - [x] If code-only model detected, return immediately with switch suggestion.
    - [x] User can override with `force: true` parameter.
    - [x] Regular `cercano_research` keeps post-run note (it's fast).
- [ ] ~~Task: Add `research_model` config field.~~ Removed — we check what's being used, not bake specific models.

## Phase 1: Multi-Pass Analysis Pipeline

### Objective
Replace the single overloaded analysis call with three focused passes.

### Tasks
- [x] Task: Implement Pass 1 — Fact Extraction (`ExtractFacts`).
    - [x] Example-driven prompt with BAD vs GOOD examples.
    - [x] Red/Green TDD: TestExtractFacts_ReturnsBullets.
- [x] Task: Implement Pass 2 — Relevance Analysis (`AnalyzeRelevance`).
    - [x] Takes extracted facts + intent + cross-context.
    - [x] Example-driven prompt for WHY_IT_MATTERS and HOW_TO_USE.
    - [x] Scoring guide with full range descriptions (1-5).
    - [x] Red/Green TDD: TestAnalyzeRelevance_ParsesScores.
- [x] Task: Implement Pass 3 — Quality Gate (`ScoreQuality`).
    - [x] Checks for specific facts vs vague filler.
    - [x] Re-prompts fact extraction with critique on failure (max 1 retry).
    - [x] Red/Green TDD: TestScoreQuality_PassesGoodSummary, TestScoreQuality_FailsVagueSummary.
- [x] Task: Wire three passes into `AnalyzeFinding`.

## Phase 2: Cross-Finding Context

### Objective
Give the model awareness of previously analyzed findings.

### Tasks
- [x] Task: `BuildCrossContext` — 1-line summary per prior finding, capped at 15.
    - [x] Red/Green TDD: TestBuildCrossContext_FormatsCorrectly, TestBuildCrossContext_CapsAt15.
- [x] Task: Pass cross-context to AnalyzeRelevance.
- [x] Task: `AnalyzeAll` accumulates context as it processes.

## Phase 3: Quality Gate with Re-Prompting

### Tasks
- [x] Implemented as part of Phase 1 (Pass 3).

## Phase 4: Depth Over Breadth

### Tasks
- [x] Task: Updated `DefaultConfig` — survey: 3/source, thorough: 6/source, increased truncation limits.
- [x] Task: Updated planner prompt — 3-5 sources with SPECIFIC query examples (BAD vs GOOD).

## Phase 5: Example-Driven Prompts

### Tasks
- [x] Task: Examples in fact extraction prompt.
- [x] Task: Examples in relevance analysis prompt.
- [x] Task: Examples in planner prompt (search query quality).

## Phase 6: Progress Updates

### Objective
Show the user what's happening during long-running research.

### Tasks
- [x] Task: `ProgressWriter` — writes to stderr and `status.md` in output dir.
    - [x] Shows phase, detail, and elapsed time.
    - [x] status.md updated live (viewable in VS Code).
    - [x] Cleaned up on completion.
- [x] Task: `AnalyzeAllWithProgress` — per-finding progress messages.
- [x] Task: Pipeline wired with progress at every phase.

## Phase 7: Integration & Validation

### Objective
Verify the enhanced pipeline produces measurably better output.

### Tasks
- [ ] Task: Run before/after comparison.
    - [ ] Same topic + intent, compare: summary specificity, score distribution, cross-references.
- [ ] Task: Update SKILL.md with new behavior notes.
- [ ] Task: Conductor - User Manual Verification 'Integration & Validation' (Protocol in workflow.md)
