# Track Plan: Web Research Tool

## Phase 1: Design & Architecture

### Objective
Define tool interfaces, search provider abstraction, and concurrency model.

### Tasks
- [x] Task: Define `cercano_fetch` tool interface — params, output format, size limits, error handling.
- [x] Task: Define `cercano_research` tool interface — params, multi-step flow, output format with citations.
- [x] Task: Design search provider interface — abstract DDG behind a provider so Brave/SearXNG can be added later.
- [x] Task: Design the Python subprocess protocol — CLI args, JSON output format, error signaling.
- [x] Task: Write architecture decision document (spec.md covers this).
- [-] Task: Conductor - User Manual Verification 'Design & Architecture' *(spec reviewed and approved by user)*

## Phase 2: URL Fetching (cercano_fetch)

### Objective
Build the URL fetching tool — HTTP GET + HTML-to-text extraction. No search provider needed.

### Tasks
- [x] Task: Implement HTML-to-text extractor — strip scripts, styles, nav, ads; preserve paragraph structure.
- [x] Task: Implement HTTP fetcher with timeout, redirect following, User-Agent, content-type checking.
- [x] Task: Add `cercano_fetch` MCP tool handler — accepts URL, returns raw extracted text (not summarized — host decides what to do with it).
- [-] Task: Add telemetry for fetch events *(fetch doesn't call the local model — no tokens to track)*.
- [x] Task: Write Agent Skill (SKILL.md) for `cercano_fetch`.
- [x] Task: Red/Green TDD for fetcher and extractor.
- [ ] Task: Conductor - User Manual Verification 'URL Fetching' (Protocol in workflow.md)

## Phase 3: Setup & Python Search Integration [checkpoint: 585c826]

### Objective
Set up the Python venv and DDG search script, integrate with `cercano setup`.

### Tasks
- [x] Task: Write `ddg_search.py` script — accepts query + max_results args, outputs JSON to stdout using `ddgs` lib. `6649191`
- [x] Task: Add venv creation to `cercano setup` — create `~/.config/cercano/venv/`, install `ddgs`, validate with test import. `f9cd26a`
- [x] Task: Add venv check to `cercano_research` handler — if venv missing, return error suggesting `cercano setup`. `f9cd26a`
- [x] Task: Add venv check to `cercano_init` — if research features need the venv, nudge during init. `f9cd26a`
- [x] Task: Implement Go subprocess caller — spawn `~/.config/cercano/venv/bin/python3 ddg_search.py`, parse JSON stdout, handle errors. `6649191`
- [x] Task: Red/Green TDD for subprocess integration. `6649191`
- [x] Task: Conductor - User Manual Verification 'Setup & Python Search' (Protocol in workflow.md) `585c826`

## Phase 4: Research Tool (cercano_research)

### Objective
Build the full research pipeline — query crafting, search, fetch, analyze, synthesize.

### Tasks
- [x] Task: Implement query crafting — local model generates 2-3 search queries from the user's question. `e7f0b0c`
- [x] Task: Implement parallel search execution — spawn DDG searches concurrently via goroutines. `e7f0b0c`
- [x] Task: Implement result deduplication and ranking — merge results from multiple queries, remove duplicates by URL. `e7f0b0c`
- [x] Task: Implement parallel URL fetching — fetch top N pages concurrently. `e7f0b0c`
- [x] Task: Implement analysis and synthesis — local model reads fetched content and produces a sourced answer. `e7f0b0c`
- [x] Task: Add `cercano_research` MCP tool handler — orchestrates the full pipeline. Support batch mode (multiple questions in one call). `e7f0b0c`
- [x] Task: Add telemetry for research events (token_saving=true). `e7f0b0c`
- [x] Task: Red/Green TDD. `e7f0b0c`
- [x] Task: Write Agent Skill (SKILL.md) for both `cercano_fetch` and `cercano_research`. `ca8a8f2`
- [x] Task: Update README.md with research tool documentation. `ca8a8f2`
- [ ] Task: Conductor - User Manual Verification 'Research Tool' (Protocol in workflow.md)

## Phase 5: Pluggable Search Providers (future)

### Objective
Add support for additional search providers beyond DuckDuckGo.

### Tasks
- [ ] Task: Document the search provider interface — how to add a new provider.
- [ ] Task: Add Brave Search API provider — requires API key in config.
- [ ] Task: Add SearXNG provider — connects to self-hosted instance.
- [ ] Task: Add `search_provider` config option to `cercano_config`.
- [ ] Task: Update README.md with provider setup instructions.
- [ ] Task: Conductor - User Manual Verification 'Pluggable Providers' (Protocol in workflow.md)
