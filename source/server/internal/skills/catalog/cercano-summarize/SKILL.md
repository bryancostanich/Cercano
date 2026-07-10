---
name: cercano-summarize
description: Summarize text or files using local AI via Cercano without sending content to the cloud. Supports brief, medium, and detailed summary lengths.
TRIGGER when: user asks to summarize a file, log, diff, document, or large block of text. Use this INSTEAD of reading the full content into cloud context.
DO NOT TRIGGER when: summarizing short text that's already in the conversation context.
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
