# Track Specification: Web Research Tool

## 1. Job Title
Add a web research capability to Cercano that fetches URLs, searches the web via DuckDuckGo, and uses local models to analyze and distill results — keeping raw web content out of the cloud context window.

## 2. Overview
Cloud agents can search the web, but every result gets stuffed into the cloud context window — consuming tokens, costing money, and often adding noise. Cercano can handle the grunt work locally: fetch pages, search for information, and return distilled answers instead of raw HTML.

This track adds two tools:
- **`cercano_fetch`** — Fetch a URL and extract readable text. First-class URL fetching with HTML-to-text conversion.
- **`cercano_research`** — Web research powered by DuckDuckGo search + URL fetching + local model analysis. Given a question, it crafts queries, searches, fetches top results, and returns a synthesized answer.

### Value Proposition
- **Context savings** — Cloud agent gets a distilled answer instead of raw web pages
- **Privacy** — Browsed content stays local, never sent to cloud
- **Cost** — Local model analyzes the content, not the cloud model
- **No API keys required** — DuckDuckGo search, no account needed

## 3. Architecture

```
Host AI: "cercano_research: what's the Ollama API for listing models?"
    │
    ▼
┌─────────────────────────────────────────────┐
│           Cercano MCP Server (Go)           │
│                                             │
│  1. Send prompt to local model:             │
│     "Craft 2-3 search queries for this"     │
│                                             │
│  2. For each query (parallel goroutines):   │
│     └→ Subprocess: python3 ddg_search.py    │
│        (uses duckduckgo-search lib)         │
│        Returns: [{url, title, snippet}...]  │
│                                             │
│  3. Fetch top URLs (parallel goroutines):   │
│     └→ HTTP GET + HTML-to-text extraction   │
│                                             │
│  4. Send fetched content to local model:    │
│     "Analyze these results and answer the   │
│      original question"                     │
│                                             │
│  5. Return distilled answer to host         │
└─────────────────────────────────────────────┘
```

Key decisions:
- **DuckDuckGo as default search** — Zero config, no API key, free. Scraped via the well-maintained `ddgs` Python library.
- **Python subprocess for search** — Go spawns `python3 ddg_search.py`, reads JSON from stdout. ~70ms startup overhead, negligible vs network latency.
- **Bundled Python venv** — `cercano setup` creates `~/.config/cercano/venv/` with `ddgs` installed. Isolated from system Python.
- **Go for everything else** — HTTP fetching, HTML-to-text, concurrency orchestration, local model calls all in Go.
- **Pluggable search providers** — Architecture supports adding Brave, SearXNG, etc. later via a provider interface.

## 4. Requirements

### 4.1 cercano_fetch
- Input: `url` (required), `project_dir` (optional for context)
- Behavior: HTTP GET, extract readable text from HTML (strip tags, scripts, styles, nav), respect robots.txt
- Output: Full extracted text content (no artificial truncation — host decides what to use)
- Error handling: Timeouts, redirects, non-HTML content types

### 4.2 cercano_research
- Input: `query` (required — the research question), `max_results` (optional, default 5), `project_dir` (optional for context)
- Behavior:
  1. Local model crafts 2-3 search queries from the user's question
  2. Execute searches via DuckDuckGo (parallel)
  3. Deduplicate and rank results
  4. Fetch top N pages (parallel)
  5. Local model analyzes fetched content and synthesizes answer
- Output: Distilled answer with source URLs cited

### 4.3 Python Search Script
- Location: `source/server/scripts/ddg_search.py`
- Interface: `python3 ddg_search.py --query "..." --max-results N`
- Output: JSON array of `{url, title, snippet}` to stdout
- Dependencies: `ddgs` library installed in venv
- Error handling: Print error to stderr, exit non-zero

### 4.4 Setup Integration
- `cercano setup` creates `~/.config/cercano/venv/` if not present
- Installs `ddgs` into the venv
- Validates the venv works by running a test import

## 5. Acceptance Criteria
- [ ] `cercano_fetch` correctly fetches and extracts text from real URLs
- [ ] `cercano_research` returns useful, sourced answers to research questions
- [ ] Works out of the box after `cercano setup` — no manual Python setup
- [ ] Parallel fetching of multiple URLs
- [ ] Graceful degradation if search fails (return what was found)
- [ ] Telemetry events emitted for both tools

## 6. Out of Scope
- JavaScript rendering (static HTML only, same as Claude's WebFetch)
- Image/media search
- Caching of fetched pages (future enhancement)
- Search providers other than DuckDuckGo (Phase 5 — future)

## 7. Future Consideration: .NET AOT Search Binary
The Python venv for DDG search is ~49MB. A .NET AOT console app doing the same HTTP + HTML parsing (using AngleSharp) would be 2-5MB, start in milliseconds, and require no runtime. No good .NET DDG library exists today, but the scraping is straightforward. Worth considering if the Python dependency becomes a pain point.
