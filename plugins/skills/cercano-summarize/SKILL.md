---
name: cercano-summarize
description: Use when the user needs to summarize large text, files, logs, or diffs. ALWAYS prefer this over reading large files directly into cloud context. Processes content locally and returns a concise summary.
compatibility: Requires Cercano server running and connected to an Ollama instance.
---

# Cercano Summarize

Summarize text or files using local AI inference.

## MCP Tool

**Tool name:** `cercano_summarize`

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `text` | string | No* | Raw text to summarize. Provide either `text` or `file_path`. |
| `file_path` | string | No* | Path to a file to read and summarize. Provide either `text` or `file_path`. |
| `max_length` | string | No | Target summary length: `"brief"` (1-2 sentences), `"medium"` (1 paragraph, default), or `"detailed"` (multiple paragraphs). |

*One of `text` or `file_path` is required.

## Examples

**Summarize text:**
```json
{
  "text": "The quick brown fox... [long text here]",
  "max_length": "brief"
}
```

**Summarize a file:**
```json
{
  "file_path": "/project/docs/architecture.md",
  "max_length": "detailed"
}
```
