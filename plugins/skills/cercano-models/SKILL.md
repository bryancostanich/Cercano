---
name: cercano-models
description: Use when the user wants to see what AI models are available on their Ollama instance. Returns model names, sizes, and modification dates.
compatibility: Requires Cercano server running and connected to an Ollama instance.
---

# Cercano Models

List all models available on the active Ollama instance.

## MCP Tool

**Tool name:** `cercano_models`

## Parameters

None. This tool takes no parameters.

## Response

Returns a formatted list of available models with:
- Model name
- Size (in GB)
- Last modified date

## Example

**Request:**
```json
{}
```

**Response:**
```
Available models:
- qwen2.5-coder:32b (18.5 GB, modified 2026-03-15)
- llama3.2:latest (4.7 GB, modified 2026-03-10)
- codellama:13b (7.4 GB, modified 2026-03-01)
```
