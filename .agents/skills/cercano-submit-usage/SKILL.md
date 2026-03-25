---
name: cercano-submit-usage
description: Submit cloud token usage data to Cercano (opt-in). This sends data, not a report — use cercano_stats to view usage. Helps track cloud tokens alongside local inference for accurate local-vs-cloud comparison.
compatibility: Requires Cercano server running with telemetry enabled.
---

# Cercano Submit Usage

Submit cloud token usage data to Cercano for local-vs-cloud comparison. This tool *sends* data — to *view* usage reports, use `cercano_stats` instead.

In most setups, the PostToolUse hook handles this automatically. This tool is for manual or programmatic submission.

## MCP Tool

**Tool name:** `cercano_submit_usage`

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `cloud_input_tokens` | integer | Yes | Number of tokens sent to the cloud model. |
| `cloud_output_tokens` | integer | Yes | Number of tokens received from the cloud model. |
| `cloud_provider` | string | No | Cloud provider name (e.g. `"anthropic"`, `"google"`). |
| `cloud_model` | string | No | Cloud model name (e.g. `"claude-opus-4-6"`, `"gemini-3-flash"`). |

## Examples

**Submit cloud usage:**
```json
{
  "cloud_input_tokens": 15000,
  "cloud_output_tokens": 3000,
  "cloud_provider": "anthropic",
  "cloud_model": "claude-opus-4-6"
}
```
