---
name: cercano-classify
description: Classify or triage text using local AI via Cercano. Returns a category, confidence level, and brief reasoning. Use this for quick local triage of errors, logs, code quality issues, bug reports, or any content that needs categorization without sending it to the cloud.
compatibility: Requires Cercano server running and connected to an Ollama instance.
---

# Cercano Classify

Classify or triage text into categories using local AI inference.

## MCP Tool

**Tool name:** `cercano_classify`

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `text` | string | Yes | The text to classify or triage. |
| `categories` | string | No | Comma-separated list of categories to choose from. If omitted, the model determines appropriate categories. |

## Response

Returns a structured classification:
- **Category:** The determined category
- **Confidence:** high, medium, or low
- **Reasoning:** One sentence explanation

## Examples

**Classify an error with predefined categories:**
```json
{
  "text": "FATAL: password authentication failed for user \"postgres\"",
  "categories": "auth,network,configuration,data,unknown"
}
```

**Open-ended classification:**
```json
{
  "text": "The API returns 200 but the response body is empty when the user has no profile picture set"
}
```
