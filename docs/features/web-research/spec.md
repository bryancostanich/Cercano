# Web Research Tool

## Overview
Cloud agents can search the web, but every result gets stuffed into the cloud context window — consuming tokens, costing money, and adding noise. This feature lets Cercano do the grunt work locally: fetch pages, search the web via DuckDuckGo, and return distilled answers instead of raw HTML. It adds two MCP tools — `cercano_fetch` (fetch a URL and extract readable text) and `cercano_research` (DuckDuckGo search + URL fetching + local-model analysis to synthesize a sourced answer). The payoff is context savings (distilled answers, not raw pages), privacy (browsed content stays local), cost (the local model does the analysis), and no API keys required.

## Design / Architecture
Go handles HTTP fetching, HTML-to-text extraction, concurrency orchestration, and local-model calls. Search uses DuckDuckGo as the zero-config default, scraped via the `ddgs` Python library run as a subprocess.

```
Host AI: cercano_research(question)
 → local model crafts 2-3 search queries
 → for each query (parallel goroutines): subprocess python3 ddg_search.py
      → [{url, title, snippet}...]
 → dedupe + rank results
 → fetch top N URLs (parallel goroutines): HTTP GET + HTML-to-text
 → local model analyzes fetched content and synthesizes a sourced answer
 → return distilled answer to host
```

Key decisions: DuckDuckGo as default (no API key, free); a Python subprocess for search (Go spawns `python3 ddg_search.py`, reads JSON from stdout — ~70ms startup overhead, negligible vs network latency); a bundled, isolated Python venv at `~/.config/cercano/venv/` created by `cercano setup` with `ddgs` installed; and a pluggable search-provider interface so Brave / SearXNG can be added later. Relevant code lives in `source/server/internal/web/` with the search script at `source/server/scripts/ddg_search.py`.

## Key behaviors / capabilities
- **`cercano_fetch`** — input `url` (required) plus optional `project_dir` for context. Does an HTTP GET with timeout, redirect following, User-Agent, and content-type checking, then extracts readable text (strips tags, scripts, styles, nav, ads while preserving paragraph structure) and returns the full extracted text with no artificial truncation — the host decides what to use. Has its own Agent Skill (SKILL.md). No telemetry (it doesn't call the local model, so there are no tokens to track).
- **`cercano_research`** — input `query` (required), `max_results` (default 5), optional `project_dir`. Runs the full pipeline: query crafting, parallel DDG search, dedupe/rank by URL, parallel page fetching, then local-model analysis and synthesis producing an answer with cited source URLs. Supports batch mode (multiple questions in one call) and emits telemetry events flagged `token_saving=true`.
- **Python search script** — `python3 ddg_search.py --query "..." --max-results N`, outputs a JSON array of `{url, title, snippet}` to stdout; errors to stderr with non-zero exit.
- **Setup integration** — `cercano setup` creates the venv if absent, installs `ddgs`, and validates it with a test import. `cercano_research` and `cercano_init` check for the venv and nudge the user to run `cercano setup` if it's missing.
- Graceful degradation: if search partially fails, return what was found.

## Notable decisions / constraints
- Out of scope: JavaScript rendering (static HTML only, like Claude's WebFetch), image/media search, caching of fetched pages, and search providers beyond DuckDuckGo (future Phase 5 — Brave API and SearXNG, with a `search_provider` config option).
- The Python venv for DDG search is ~49MB; a future .NET AOT search binary (HTTP + AngleSharp HTML parsing) would be 2-5MB and start in milliseconds with no runtime — noted as worth considering if the Python dependency becomes a pain point, though no good .NET DDG library exists today.
