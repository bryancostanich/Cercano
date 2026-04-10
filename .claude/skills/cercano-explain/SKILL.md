---
name: cercano-explain
description: Explain code or text using local AI via Cercano. Returns a clear explanation of what the code does, its key interfaces, and data flow. Use this to understand unfamiliar code, complex algorithms, or dense documentation locally before deciding what context to send to the cloud.
compatibility: Requires Cercano server running and connected to an Ollama instance.
---

# Cercano Explain

Explain code or text using local AI inference.

## Important: Display the result

MCP tool results may not be visible to the user in the terminal. After calling the tool, you MUST output the full tool result text verbatim in your response so the user can see it.

## MCP Tool

**Tool name:** `cercano_explain`

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `text` | string | No* | Code or text to explain. Provide either `text` or `file_path`. |
| `file_path` | string | No* | Path to a file to read and explain. Provide either `text` or `file_path`. |

*One of `text` or `file_path` is required.

## Response

Returns an explanation covering:
- What the code/text does (high-level purpose)
- Key interfaces and components
- Data flow

## Examples

**Explain code inline:**
```json
{
  "text": "func (s *Server) handleLocal(ctx context.Context, request *gomcp.CallToolRequest, args LocalRequest) (*gomcp.CallToolResult, any, error) { ... }"
}
```

**Explain a file:**
```json
{
  "file_path": "/project/internal/agent/router.go"
}
```
