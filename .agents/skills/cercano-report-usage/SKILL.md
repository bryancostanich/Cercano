---
name: cercano-report-usage
description: Report cloud token usage from the host agent (opt-in). Call this to help Cercano track how many cloud tokens are used alongside local inference, enabling accurate local-vs-cloud usage comparison.
compatibility: Requires Cercano server running with telemetry enabled.
---

# Cercano Report Usage

Report cloud token usage from the host agent to Cercano for local-vs-cloud comparison.

## MCP Tool

**Tool name:** `cercano_report_usage`

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `cloud_input_tokens` | integer | Yes | Number of tokens sent to the cloud model. |
| `cloud_output_tokens` | integer | Yes | Number of tokens received from the cloud model. |
| `cloud_provider` | string | No | Cloud provider name (e.g. `"anthropic"`, `"google"`). |
| `cloud_model` | string | No | Cloud model name (e.g. `"claude-opus-4-6"`, `"gemini-3-flash"`). |

## Examples

**Report cloud usage:**
```json
{
  "cloud_input_tokens": 15000,
  "cloud_output_tokens": 3000,
  "cloud_provider": "anthropic",
  "cloud_model": "claude-opus-4-6"
}
```
