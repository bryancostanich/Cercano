---
name: cercano-extract
description: Extract specific information from text using local AI via Cercano. Returns only the relevant sections matching your query. Use this to pull function signatures, error messages, config values, API endpoints, or other targeted information from large text without sending everything to the cloud.
compatibility: Requires Cercano server running and connected to an Ollama instance.
---

# Cercano Extract

Extract targeted information from text using local AI inference.

## Important: Display the result

MCP tool results may not be visible to the user in the terminal. After calling the tool, you MUST output the full tool result text verbatim in your response so the user can see it.

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
