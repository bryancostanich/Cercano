# Track Plan: Web Research Tool

## Phase 1: Design & Architecture

### Objective
Define tool interfaces, search provider abstraction, and concurrency model.

### Tasks
- [ ] Task: Define `cercano_fetch` tool interface — params, output format, size limits, error handling.
- [ ] Task: Define `cercano_research` tool interface — params, multi-step flow, output format with citations.
- [ ] Task: Design search provider interface — abstract DDG behind a provider so Brave/SearXNG can be added later.
- [ ] Task: Design the Python subprocess protocol — CLI args, JSON output format, error signaling.
- [ ] Task: Write architecture decision document (spec.md covers this).
- [ ] Task: Conductor - User Manual Verification 'Design & Architecture' (Protocol in workflow.md)

## Phase 2: URL Fetching (cercano_fetch)

### Objective
Build the URL fetching tool — HTTP GET + HTML-to-text extraction. No search provider needed.

### Tasks
- [ ] Task: Implement HTML-to-text extractor — strip scripts, styles, nav, ads; preserve paragraph structure.
- [ ] Task: Implement HTTP fetcher with timeout, redirect following, User-Agent, content-type checking.
- [ ] Task: Add `cercano_fetch` MCP tool handler — accepts URL, returns raw extracted text (not summarized — host decides what to do with it).
- [ ] Task: Add telemetry for fetch events (token_saving=true).
- [ ] Task: Write Agent Skill (SKILL.md) for `cercano_fetch`.
- [ ] Task: Red/Green TDD for fetcher and extractor.
- [ ] Task: Conductor - User Manual Verification 'URL Fetching' (Protocol in workflow.md)

## Phase 3: Setup & Python Search Integration

### Objective
Set up the Python venv and DDG search script, integrate with `cercano setup`.

### Tasks
- [ ] Task: Write `ddg_search.py` script — accepts query + max_results args, outputs JSON to stdout using `duckduckgo-search` lib.
- [ ] Task: Add venv creation to `cercano setup` — create `~/.config/cercano/venv/`, install `duckduckgo-search`, validate with test import.
- [ ] Task: Add venv check to `cercano_research` handler — if venv missing, return error suggesting `cercano setup`.
- [ ] Task: Add venv check to `cercano_init` — if research features need the venv, nudge during init.
- [ ] Task: Implement Go subprocess caller — spawn `~/.config/cercano/venv/bin/python3 ddg_search.py`, parse JSON stdout, handle errors.
- [ ] Task: Red/Green TDD for subprocess integration.
- [ ] Task: Conductor - User Manual Verification 'Setup & Python Search' (Protocol in workflow.md)

## Phase 4: Research Tool (cercano_research)

### Objective
Build the full research pipeline — query crafting, search, fetch, analyze, synthesize.

### Tasks
- [ ] Task: Implement query crafting — local model generates 2-3 search queries from the user's question.
- [ ] Task: Implement parallel search execution — spawn DDG searches concurrently via goroutines.
- [ ] Task: Implement result deduplication and ranking — merge results from multiple queries, remove duplicates by URL.
- [ ] Task: Implement parallel URL fetching — fetch top N pages concurrently.
- [ ] Task: Implement analysis and synthesis — local model reads fetched content and produces a sourced answer.
- [ ] Task: Add `cercano_research` MCP tool handler — orchestrates the full pipeline. Support batch mode (multiple questions in one call).
- [ ] Task: Add telemetry for research events (token_saving=true).
- [ ] Task: Red/Green TDD.
- [ ] Task: Write Agent Skill (SKILL.md) for both `cercano_fetch` and `cercano_research`.
- [ ] Task: Update README.md with research tool documentation.
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
