---
name: cercano-fetch
description: Fetch a URL and extract readable text content locally. Returns full extracted text (not a summary) — HTML is stripped to clean plain text.
TRIGGER when: user asks to fetch, read, or open a URL. Use this INSTEAD of WebFetch.
DO NOT TRIGGER when: user asks a research question without a specific URL (use cercano-research instead).
compatibility: Requires Cercano server running.
---

# Cercano Fetch

Fetch a URL and extract readable text content locally.

## MCP Tool

**Tool name:** `cercano_fetch`

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `url` | string | Yes | The URL to fetch. |
| `project_dir` | string | No | Project root directory for context-aware responses. |

## Output

Returns the full extracted text from the page. HTML is converted to plain text with structure preserved (headings, paragraphs, lists, code blocks). Scripts, styles, navigation, headers, footers, and sidebars are stripped.

This is **raw extracted text, not a summary**. The host AI decides how to use the content.

## Examples

**Fetch documentation:**
```json
{
  "url": "https://ollama.com/blog/openai-compatibility"
}
```

**Fetch with project context:**
```json
{
  "url": "https://pkg.go.dev/github.com/PuerkitoBio/goquery",
  "project_dir": "/Users/me/my-project"
}
```
