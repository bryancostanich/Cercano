---
name: cercano-config
description: Query or update Cercano's runtime configuration without restarting the server. Use this to switch the active local model, change the Ollama endpoint URL, or change the cloud provider and model. Useful when you need a different model for a specific task or want to point at a different Ollama instance.
compatibility: Requires Cercano server running.
---

# Cercano Config

Query or update Cercano's runtime configuration.

## MCP Tool

**Tool name:** `cercano_config`

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `action` | string | Yes | `"get"` to query current config, `"set"` to update it. |
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
