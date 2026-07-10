---
name: cercano-config
description: Query or update Cercano's runtime configuration without restarting the server. Use action 'get' to list available local models. Use action 'set' to switch the active local runtime or model, change the Ollama endpoint URL, or change the cloud provider and model.
compatibility: Requires Cercano server running.
---

# Cercano Config

Query or update Cercano's runtime configuration.

## Important: Display the result

MCP tool results may not be visible to the user in the terminal. After calling the tool, you MUST output the full tool result text verbatim in your response so the user can see it.

## MCP Tool

**Tool name:** `cercano_config`

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `action` | string | Yes | `"get"` to list available local models, `"set"` to update config. |
| `local_runtime` | string | No | Local runtime to use for generation: `"ollama"` or `"llama_server"`. |
| `local_model` | string | No | Local model name to set (e.g. `"qwen2.5-coder:32b"`). |
| `cloud_provider` | string | No | Cloud provider to set: `"google"` or `"anthropic"`. |
| `cloud_model` | string | No | Cloud model to set (e.g. `"claude-sonnet-4-20250514"`). |
| `ollama_url` | string | No | Ollama endpoint URL (e.g. `"http://mac-studio.local:11434"`). |

## Examples

**Switch local model:**
```json
{
  "action": "set",
  "local_model": "qwen2.5-coder:32b"
}
```

**Switch local runtime:**
```json
{
  "action": "set",
  "local_runtime": "llama_server"
}
```

**Point to a different Ollama instance:**
```json
{
  "action": "set",
  "ollama_url": "http://mac-studio.local:11434"
}
```

**Switch cloud provider:**
```json
{
  "action": "set",
  "cloud_provider": "anthropic",
  "cloud_model": "claude-sonnet-4-20250514"
}
```
