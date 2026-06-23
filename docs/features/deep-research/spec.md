# Deep Research

## Overview

`cercano_deep_research` is a multi-source research tool that takes a topic and a stated intent, identifies authoritative sources for that domain, systematically searches each one, and compiles a ranked, annotated reference document. Unlike the lightweight `cercano_research` tool (single DuckDuckGo search plus a few fetches), deep research plans where to look based on domain, fetches and analyzes many sources, chases cited references, scores each finding for relevance to the user's intent, and synthesizes the whole into a structured report. The entire pipeline — source planning, searching, fetching, per-finding analysis, ranking, and synthesis — runs locally; the host agent receives only the final compiled document, making this the highest-token-savings tool in Cercano (a thorough run can avoid 100K+ cloud tokens).

## Design / Architecture

The implementation lives in `internal/research/` and runs as a six-phase pipeline driven by the local model.

**Phase 1 — Source Planning.** The local model analyzes topic + intent and produces a ranked `ResearchPlan` of `Source` entries (Name, Type api/web, Site, tailored Queries, Reason). A source registry of 25+ entries spans academic/scientific (PubMed, arXiv, bioRxiv/medRxiv, Google Scholar, ClinicalTrials.gov, IEEE Xplore, SSRN, Semantic Scholar), industry/tech (GitHub, Hacker News, Stack Overflow, patent databases), news/popular science (Wired, Ars Technica, MIT Technology Review, Nature News, NYT, etc.), reference (Wikipedia, Britannica, Stanford Encyclopedia of Philosophy), and regulatory/government (FDA, WHO, NIH). The model selects which sources fit the domain. Users may override the source list (`PlanWithOverride`), in which case the model still generates per-source queries.

**Phase 2 — Systematic Search.** A `SearchDispatcher` routes each source to the right mechanism. Sources with free public APIs (PubMed, arXiv, etc.) use those; everything else uses DuckDuckGo with `site:domain` scoping. `SearchAllSources` runs searches concurrently, collecting URLs and metadata into a unified `Publication` struct, deduplicated by URL.

**Phase 3 — Content Extraction.** For the top N results (set by `depth`), pages are fetched and readable text extracted (reusing the `web` package); PDF abstract pages yield the abstract.

**Phase 4 — Analysis & Annotation.** `AnalyzeFinding` runs each result through the local model with a section-aware multi-line parser, producing Summary, KeyFindings (bullets), WhyItMatters, HowToUse, RelevanceScore (1-5 stars), ImpactRating (low/medium/high), and CitedRefs. The prompt demands concrete facts, numbers, methods, and conclusions rather than vague descriptions. `AnalyzeAll` batches results sequentially with graceful degradation (empty content skipped).

**Phase 4b — Reference Chasing.** Cited works extracted during analysis enter a dedup'd chase queue, are located (by title on the original source, falling back to DuckDuckGo), then fetched and analyzed like primary findings and marked "discovered via [parent]". Depth is limited to 1 hop. Budget: at most 5 references per finding, and 50 total chased per run (thorough) / 10 (survey).

**Phase 5 — Synthesis & Compilation.** Findings are sorted by relevance, then the local model generates an executive summary, narrative synthesis, contradiction detection, gap analysis, recommended reading order, and suggested follow-up queries. `WriteReport` emits a multi-file report directory (README.md table of contents, `findings/`, `references/`, `source_plan.md`, `synthesis.md`); `CompileReport` produces a single-file fallback when no output directory is given.

**Phase 5b — Progress Persistence.** A checkpoint system writes intermediate state to `.cercano/research/<topic-hash>/` (plan.json, search_results.json, findings.json, sections.json). A crashed or timed-out run resumes from the last checkpoint when re-invoked with the same parameters. The work directory is cleaned up after completion unless an output directory is set.

**Phase 6 — MCP Integration.** `handleDeepResearch` registers `cercano_deep_research`. An adapter pattern (webSearchAdapter, webFetchAdapter) bridges the `web` package to the research interfaces. With an output directory it writes the multi-file report and returns a short summary; otherwise it returns the full compiled report inline. Telemetry records `content_tokens_avoided`. The tool is registered in `builtinSkills()` with a corresponding `.agents/skills/cercano-deep-research/SKILL.md`.

## Key behaviors / capabilities

- Domain-aware source selection across 25+ registered sources, with user override support.
- Free public API clients for PubMed (NCBI E-utilities, 350ms throttle / ~3 req/sec), arXiv (Atom XML), bioRxiv/medRxiv, ClinicalTrials.gov, and Semantic Scholar; site-scoped DuckDuckGo for the rest.
- Per-finding relevance scoring (1-5 stars) and impact rating tied to the user's stated intent.
- 1-hop reference chasing with per-finding and per-run budgets.
- Synthesis outputs: executive summary, narrative synthesis, contradictions/open debates, gap analysis, reading order, follow-up queries.
- Multi-file or inline report output via `output_path`/`output_dir`.
- Crash-safe checkpointing and resume.

## Parameters

- `topic` (required) — the research topic.
- `intent` (required) — what the research is for; drives relevance scoring and annotation.
- `depth` — `survey` (top 5-10, light) or `thorough` (20+, deep). Default `thorough`.
- `date_range` — filter by date (e.g. `2024-2026`, `last 2 years`).
- `sources` — override the auto-detected source list.
- `output_path` / `output_dir` — write report to disk instead of returning inline (recommended for thorough runs to keep the MCP response small).
- `project_dir` — project root for context.

## Notable decisions / constraints

- Reference chasing capped at 1 hop to prevent exponential blowup while still catching key related work.
- Latency: a thorough run with chasing can take several minutes (many fetches + many model calls); progress persistence mitigates loss.
- Rate limiting enforced for PubMed (350ms between requests).
- Star ratings are local-model estimates, not ground truth.
- Some academic sites block scraping; failures are skipped gracefully and noted.
- `output_path` is important for keeping the MCP response small on large reports.

## Non-goals

Full-text PDF download/analysis (abstracts and metadata only), citation-graph analysis, automated bibliography formatting (BibTeX), real-time publication monitoring, and paid API integrations (Scopus, Web of Science).
