---
name: cercano-deep-research
description: Use when the user needs thorough, multi-source research with ranked findings, citations, and synthesis. Use this for literature reviews, competitive analysis, technical deep-dives, or any research that needs more than a quick answer. Prefer this over cercano-research when depth and comprehensiveness matter.
compatibility: Requires Cercano server running, connected to an Ollama instance, and Python venv with ddgs package (run cercano setup).
---

# Cercano Deep Research

Multi-source research tool that takes a topic and intent, identifies authoritative sources, systematically searches each one, and compiles a ranked, annotated encyclopedia of findings.

## MCP Tool

**Tool name:** `cercano_deep_research`

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| topic | string | Yes | The research topic to investigate. |
| intent | string | Yes | What you need this research for — drives relevance scoring and source selection. |
| depth | string | No | `"survey"` (quick scan, ~2 min), `"standard"` (balanced, ~5-8 min), or `"deep"` (exhaustive, ~15+ min). Default: `"standard"`. |
| date_range | string | No | Filter results by date (e.g. `"2024-2026"`, `"last 2 years"`). |
| sources | string[] | No | Override auto-detected sources. If omitted, sources are chosen based on topic domain. |
| output_dir | string | No | Write report to this directory as multiple files. Recommended for standard/deep research and required for incremental deepening. |
| project_dir | string | No | Project root directory. |
| phase | string | No | Run a specific phase: `"plan"`, `"search"`, `"analyze"`, `"synthesize"`. Omit to run all phases. |
| use_model | string | No | Override the default model for this research run. |

## Depth Tiers

| | Survey | Standard (default) | Deep |
|---|---|---|---|
| Sources | 2-3 | 3-4 | 4-5 |
| Results/query | 3 | 4 | 6 |
| Reference chasing | None | 1-hop, max 15 | 1-hop, max 50 |
| Analysis | 3-pass (facts, relevance, quality gate) | 3-pass | 3-pass |
| Target time | ~2 min | ~5-8 min | ~15+ min |

## How It Works

1. **Source Planning** — Local model analyzes topic + intent and identifies relevant sources from 25+ options across academic, industry, news, reference, and regulatory categories
2. **Systematic Search** — Searches each source using tailored queries (free APIs for PubMed, arXiv; site-scoped DuckDuckGo for others)
3. **Content Extraction** — Fetches and extracts readable content from top results
4. **Analysis & Annotation** — 3-pass pipeline: fact extraction, relevance scoring (1-5), quality gate with retry
5. **Reference Chasing** (standard/deep only) — Identifies cited works relevant to intent, searches and analyzes them
6. **Synthesis** — Executive summary, narrative synthesis, contradiction detection, gap analysis, reading order, follow-up suggestions

## Incremental Deepening

Run a survey first to get a quick landscape, then deepen to standard or deep without re-doing prior work:

1. Run survey with an `output_dir`
2. Review the results
3. Run again with the same `output_dir` and a deeper depth — existing findings are preserved, new sources are added, and middle-scored findings (2-4) are re-evaluated with richer context

The tool stores state in `research_state.json` inside the output directory. Each deepening pass expands the plan with complementary sources and enriches the analysis.

## Output

Structured markdown report with:
- Executive Summary (TL;DR)
- Source Plan (which sources were searched and why)
- Ranked Findings (sorted by relevance, with annotations)
- Discovered References (works found via citation chasing)
- Synthesis (narrative connecting the findings)
- Contradictions & Open Debates
- Gap Analysis (what the research didn't find)
- Recommended Reading Order
- Suggested Follow-Up Research
- Next Steps (suggested deeper research command)

## Examples

**Quick survey:**
```json
{"topic": "quantum computing error correction", "intent": "preparing a conference talk", "depth": "survey", "output_dir": "/tmp/qec-research"}
```

**Standard research (default):**
```json
{"topic": "CRISPR gene therapy for sickle cell disease", "intent": "writing a grant proposal for a novel delivery mechanism", "output_dir": "/tmp/crispr-research"}
```

**Deep research:**
```json
{"topic": "transformer architecture improvements", "intent": "literature review for PhD thesis", "depth": "deep", "date_range": "2024-2026", "output_dir": "/tmp/transformer-research"}
```

**Incremental deepening (survey → standard):**
```json
{"topic": "quantum computing error correction", "intent": "preparing a conference talk", "depth": "standard", "output_dir": "/tmp/qec-research"}
```
