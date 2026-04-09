---
name: cercano-extract
description: Use when the user needs to pull specific information from large text — function signatures, error messages, config values, API endpoints. Extracts locally instead of reading entire files into cloud context.
compatibility: Requires Cercano server running and connected to an Ollama instance.
---

# Cercano Extract

Extract targeted information from text using local AI inference.

## MCP Tool

**Tool name:** `cercano_extract`

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `text` | string | Yes | The text to search through and extract information from. |
| `query` | string | Yes | What to find or extract (e.g. `"error messages"`, `"function signatures"`, `"config values"`). |

## Examples

**Extract error messages from logs:**
```json
{
  "text": "[2026-03-20 10:15:32] INFO Starting server...\n[2026-03-20 10:15:33] ERROR Failed to connect to database\n...",
  "query": "error messages"
}
```

**Extract function signatures from code:**
```json
{
  "text": "package main\n\nfunc ProcessRequest(ctx context.Context, req *Request) (*Response, error) { ... }",
  "query": "function signatures with their parameter types"
}
```
