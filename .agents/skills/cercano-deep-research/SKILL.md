---
name: cercano-deep-research
description: Deep multi-source research tool that identifies authoritative sources, systematically searches, analyzes and ranks findings, chases cited references, and compiles a structured report with executive summary, contradiction detection, gap analysis, and follow-up suggestions.
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
| depth | string | No | `"survey"` (5-10 results, quick) or `"thorough"` (20+ results, deep). Default: `"thorough"`. |
| date_range | string | No | Filter results by date (e.g. `"2024-2026"`, `"last 2 years"`). |
| sources | string[] | No | Override auto-detected sources. If omitted, sources are chosen based on topic domain. |
| output_path | string | No | Write report to file instead of returning inline. Recommended for thorough research. |
| project_dir | string | No | Project root directory. |

## How It Works

1. **Source Planning** — Local model analyzes topic + intent and identifies relevant sources from 25+ options across academic, industry, news, reference, and regulatory categories
2. **Systematic Search** — Searches each source using tailored queries (free APIs for PubMed, arXiv; site-scoped DuckDuckGo for others)
3. **Content Extraction** — Fetches and extracts readable content from top results
4. **Analysis & Annotation** — Local model analyzes each finding: summary, relevance to intent, how to use it, star rating (1-5), impact rating
5. **Reference Chasing** — Identifies cited works in findings that are relevant to intent, searches for and analyzes them (1 hop, max 50)
6. **Synthesis** — Executive summary, narrative synthesis, contradiction detection, gap analysis, recommended reading order, follow-up suggestions

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

## Examples

**Quick survey:**
```json
{"topic": "quantum computing error correction", "intent": "preparing a conference talk", "depth": "survey"}
```

**Thorough research to file:**
```json
{"topic": "CRISPR gene therapy for sickle cell disease", "intent": "writing a grant proposal for a novel delivery mechanism", "depth": "thorough", "output_path": "/tmp/crispr-research.md"}
```

**With date filter and source override:**
```json
{"topic": "transformer architecture improvements", "intent": "literature review for PhD thesis", "date_range": "2024-2026", "sources": ["arXiv", "Google Scholar", "Semantic Scholar"]}
```
