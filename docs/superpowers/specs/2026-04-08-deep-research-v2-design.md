# Deep Research v2: Three-Tier Incremental Research

**Date:** 2026-04-08
**Status:** Approved
**Track:** deep_research_enhancement_20260329 (continuation)

## Overview

Redesign the deep research pipeline to support three research tiers (survey, standard, deep) with incremental deepening — running a survey first, then expanding to standard or deep without re-doing prior work. Includes a progress tracker for user feedback, and fixes four known bugs from the Phase 9 validation.

## Goals

1. Three distinct research tiers with appropriate speed/depth trade-offs
2. Incremental deepening: survey output becomes the base for standard, which becomes the base for deep
3. Granular progress feedback via `status.md` and stderr
4. Fix score calibration, DDG result overflow, irrelevant queries, and thin content waste

## Non-Goals (Deferred to Plugin Track)

- MCP notification/streaming (broken in Claude Code today)
- `cercano_research_status` poll tool
- Push-based progress to host agent
- Claude plugin or Gemini extension packaging
- Adoption/discoverability improvements

---

## Design

### 1. Three-Tier Research Configuration

Rename existing depth levels. `standard` replaces `thorough` as the new default.

| Parameter | Survey | Standard (default) | Deep |
|---|---|---|---|
| Sources selected | 2-3 | 3-4 | 4-5 |
| Queries per source | 1-2 | 2-3 | 3 |
| Results per query | 3 | 4 | 6 |
| Page truncate chars | 8,000 | 10,000 | 12,000 |
| Analysis truncate chars | 10,000 | 12,000 | 15,000 |
| Analysis passes | 3 (facts + relevance + quality gate) | 3 | 3 |
| Reference chasing | none | 1-hop, max 15 total, max 3/finding | 1-hop, max 50 total, max 5/finding |
| Est. LLM calls | ~20-30 | ~50-80 | ~200+ |
| Target time | ~2 min | ~5-8 min | ~15+ min |

All three tiers keep the full 3-pass analysis pipeline (fact extraction, relevance scoring, quality gate). No tier produces unranked or unscored output.

The existing `survey` config is updated with reduced source/query counts. The existing `thorough` config is removed and replaced by `standard` and `deep`.

### 2. State Persistence via Sidecar

The existing ephemeral checkpoint system (writes to `.cercano/research/{hash}/`, deleted after completion) is replaced by a persistent `research_state.json` sidecar file in the output directory.

#### Sidecar Schema

```json
{
  "version": 1,
  "depth": "survey",
  "topic": "...",
  "intent": "...",
  "date_range": "...",
  "plan": {
    "sources": [
      {
        "name": "arXiv",
        "reason": "...",
        "queries": ["query1", "query2"]
      }
    ]
  },
  "search_results": [
    {
      "title": "...",
      "url": "...",
      "source": "arXiv",
      "date": "...",
      "authors": "...",
      "dedup_key": "..."
    }
  ],
  "content_cache": {
    "https://example.com/paper": "truncated extracted text..."
  },
  "findings": [
    {
      "publication": { "..." },
      "summary": "...",
      "key_findings": ["..."],
      "why_it_matters": "...",
      "how_to_use": "...",
      "relevance_score": 4,
      "impact_rating": "high",
      "quality_passed": true,
      "discovered_via": "",
      "cited_refs": ["..."]
    }
  ],
  "progress": {
    "phase": "complete",
    "step": "",
    "current": 0,
    "total": 0,
    "findings_accepted": 15,
    "run_started_at": "...",
    "phase_started_at": "...",
    "completed_at": "..."
  },
  "created_at": "2026-04-08T10:00:00Z",
  "updated_at": "2026-04-08T10:05:00Z"
}
```

The sidecar is written after every meaningful step (phase transition, finding completion) for crash recovery. On successful completion, `progress.phase` is set to `"complete"`.

The existing `checkpoint.go` is refactored to read/write this sidecar format instead of the ephemeral directory structure.

### 3. Incremental Deepening Flow

When `cercano_deep_research` is called with an `output_dir` containing an existing `research_state.json`:

1. **Load existing state.** Read and validate the sidecar. Check `version` field for compatibility.

2. **Compare depths.** If requested depth <= existing depth, return the existing report with a note ("Research already at this depth or deeper. To re-run from scratch, delete research_state.json."). Depth ordering: survey < standard < deep.

3. **Expand the plan.** Re-run the planner with the new tier's source/query counts. The planner prompt includes the list of already-searched sources and queries, instructing the model to pick complementary sources rather than duplicating. Existing sources and queries are preserved in the plan; new ones are appended.

4. **Search new queries only.** Skip queries already present in `plan.sources[].queries` from the loaded sidecar (exact string match). Execute only new queries, deduplicate results against existing URLs in `search_results`.

5. **Fetch new content only.** Only fetch URLs not already present in `content_cache`.

6. **Analyze new publications.** Run the full 3-pass pipeline (facts, relevance, quality gate) on new publications. Cross-context includes summaries from existing findings.

7. **Selective re-analysis of existing findings.** Re-run Pass 2 (relevance scoring) only on existing findings with scores 2-4. These are the "uncertain middle" where richer cross-context from new findings might change the assessment. Findings scored 1 or 5 keep their scores — they're already decided. Pass 1 (fact extraction) is never re-run because the source content hasn't changed.

8. **Chase references (if new tier allows).** Standard and deep tiers chase cited references up to their configured limits. Skip URLs already in the content cache or search results. Run full 3-pass analysis on new chased findings.

9. **Re-synthesize.** Regenerate all synthesis sections (executive summary, narrative, contradictions, gaps, follow-up queries, reading order) with the complete finding set.

10. **Write updated output.** Overwrite markdown reports and update `research_state.json` with new depth level and findings.

### 4. Suggested Next Action

After survey or standard completes, the response includes a recommendation to go deeper.

#### In the Markdown Report

A "Next Steps" section appended to the synthesis:

```markdown
## Next Steps

This survey identified 15 findings across 3 sources. For deeper analysis with
reference chasing and cross-source synthesis, run a standard-depth research pass
on the same output directory:

    cercano_deep_research topic="..." depth="standard" output_dir="/same/path"
```

#### In the MCP Response Metadata

A structured `suggested_next` field in the tool response:

```json
{
  "suggested_next": {
    "action": "deepen",
    "tool": "cercano_deep_research",
    "params": {
      "topic": "quantum error correction",
      "intent": "original intent...",
      "depth": "standard",
      "output_dir": "/same/path"
    },
    "reason": "Survey found 15 findings across 3 sources. Standard depth adds reference chasing and broader source coverage."
  }
}
```

Deep tier does not suggest further deepening — it is the terminal level. Instead, it may suggest follow-up research queries from the gap analysis.

### 5. Progress Tracker

#### Internal Model

A `ProgressTracker` struct maintained throughout the pipeline:

```go
type ProgressTracker struct {
    Phase              string    // plan, search, analyze, synthesize
    Step               string    // sub-step (e.g., "fact_extraction", "relevance", "quality_gate")
    Current            int       // items completed in current phase
    Total              int       // total items in current phase
    FindingsAccepted   int       // running count of accepted findings
    RunStartedAt       time.Time
    PhaseStartedAt     time.Time
    ItemTimes          []time.Duration // rolling window for ETA calc
    EstRemainingSeconds int
}
```

ETA is calculated as: rolling average of last N item durations * remaining items. Simple, no fancy prediction.

#### Output Channels

1. **`status.md` in the output directory.** Overwritten after every completed finding and phase transition. Deleted on successful completion.

   ```markdown
   # Research Progress
   **Topic:** quantum error correction
   **Depth:** standard
   **Phase:** Analyzing findings (12/25)
   **Current step:** Relevance scoring
   **Elapsed:** 4m 12s | **Est. remaining:** ~6 min
   **Findings accepted:** 8
   ```

2. **stderr logging.** One line per update:
   ```
   [4m12s] ANALYZE: 12/25 findings (8 accepted, ~6 min remaining)
   ```

3. **Sidecar `progress` field.** Updated with each step for crash recovery.

#### Crash Recovery

If a run is interrupted, the next invocation with the same `output_dir` detects an in-progress state (`progress.phase` != `"complete"`) in the sidecar. It resumes from the last completed step using cached content and existing findings. For example, if analysis crashed on finding 12, it picks up at finding 13.

### 6. Bug Fixes

#### 6.1 Relevance Score Clustering

**Problem:** Scores cluster at 4/5 — poor discrimination.

**Fix:** Add explicit score anchors to the Pass 2 (relevance analysis) prompt:

- **1** — Tangentially related, no actionable connection to the research intent
- **2** — Related topic area, but doesn't address the specific question
- **3** — Addresses the question but with limited specificity or indirect evidence
- **4** — Directly relevant with specific data, methods, or conclusions
- **5** — Essential finding — primary source, strong evidence, directly answers the intent

Add calibration instruction: "Use the full range. In a typical set of findings, most should score 2-4. A score of 5 means this is one of the most important results in the entire set."

#### 6.2 DDG Returns More Than max_results

**Problem:** DuckDuckGo search returns more results than the configured `MaxPrimaryResults`.

**Fix:** After deduplication in the search dispatcher, hard-cap the results list to `MaxPrimaryResults` per query on the Go side, regardless of how many the DDG Python script returns.

#### 6.3 Broad DDG Queries Return Irrelevant Results

**Problem:** Generic queries return off-topic results.

**Fix:** Two changes:
1. **Better planner prompts.** Add instruction to the query generation prompt: "Generate specific, narrow queries. Avoid broad topic queries. Include qualifying terms that disambiguate." Include examples of good vs. bad queries.
2. **Post-search title filtering.** After collecting results, check each title for keyword overlap with the topic and intent. Drop results with zero keyword overlap before spending LLM calls on analysis.

#### 6.4 Thin Content Not Filtered Before Analysis

**Problem:** Pages with little extractable text (paywalled, error pages, failed PDF extraction) waste 3 LLM calls.

**Fix:** After fetching, check extracted content length. If below 500 characters, skip analysis entirely and log the skip. These results are not added to findings.

---

## Files to Change

| File | Changes |
|---|---|
| `types.go` | New `standard` tier config, remove `thorough`. Add `ProgressTracker` struct. Add sidecar JSON types. |
| `pipeline.go` | Incremental deepening orchestration. Load/detect existing sidecar. Progress tracker integration. |
| `checkpoint.go` | Refactor to read/write `research_state.json` sidecar. Remove ephemeral directory logic. |
| `planner.go` | Expansion planning: accept existing sources, prompt model for complementary sources. |
| `analyze.go` | Selective re-analysis (re-score findings at 2-4). Thin content filter. Score anchor prompts. |
| `search.go` | DDG result hard cap. Skip existing queries during deepening. |
| `report.go` | "Next Steps" section in markdown output. `status.md` writing/deletion. |
| `synthesis.go` | No structural changes — re-runs naturally with full finding set. |
| `progress.go` | New `ProgressTracker` implementation with ETA calculation. |
| `sources.go` | No changes expected. |
| `mcp/server.go` | `suggested_next` in response metadata. Default depth → `standard`. Accept `standard` as depth value. |
| `research_test.go` | Tests for incremental deepening, selective re-analysis, progress tracker, thin content filter. |
| `.agents/skills/cercano-deep-research/SKILL.md` | Updated depth options, incremental usage examples. |

## Prior Work to Land First

Push the 6 unpushed commits from the enhancement track (model pre-check, per-request model override, phased execution, parallel prefetching, validation results, track status update). These are the foundation this design builds on.
