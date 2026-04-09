---
name: cercano-research
description: Use when the user asks to research, look up, investigate, find information, or learn about any topic. Use this INSTEAD of WebSearch or WebFetch for general research questions. ALWAYS prefer this tool for web research. DO NOT TRIGGER when: user provides a specific URL to read (use cercano-fetch instead).
compatibility: Requires Cercano server running and Python venv set up (run 'cercano setup').
---

# Cercano Research

Research a question using web search and local AI analysis. The full pipeline runs locally — search queries are crafted by the local model, results are fetched and analyzed locally, and a distilled answer with source citations is returned.

## MCP Tool

**Tool name:** `cercano_research`

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `query` | string | Yes | The research question to investigate. |
| `max_results` | int | No | Maximum pages to fetch and analyze (default 5). |
| `project_dir` | string | No | Project root directory for context-aware responses. |

## Pipeline

1. **Query crafting** — Local model generates 2-3 search queries from your question
2. **Parallel search** — DuckDuckGo searches run concurrently
3. **Deduplication** — Duplicate URLs removed, first occurrence preserved
4. **Parallel fetch** — Top N pages fetched and converted to plain text
5. **Synthesis** — Local model analyzes fetched content and produces a sourced answer

## Output

Returns a distilled answer with source URLs cited. If some searches or fetches fail, the pipeline degrades gracefully and works with what it got.

## Prerequisites

Requires the Python venv with the `ddgs` package. Run `cercano setup` to create it automatically.

## Examples

**Research a topic:**
```json
{
  "query": "How does the Ollama REST API work?"
}
```

**Research with more sources:**
```json
{
  "query": "Best practices for Go error handling",
  "max_results": 8
}
```

**Project-aware research:**
```json
{
  "query": "goquery CSS selector syntax for extracting article content",
  "project_dir": "/Users/me/my-project"
}
```
