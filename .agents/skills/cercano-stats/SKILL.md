---
name: cercano-stats
description: View Cercano usage statistics and cloud token savings. Shows total requests, tokens processed locally, cloud tokens reported by the host, percentage kept local, and breakdowns by tool, model, and day.
compatibility: Requires Cercano server running with telemetry enabled.
---

# Cercano Stats

View usage statistics and cloud token savings from Cercano's local inference.

## MCP Tool

**Tool name:** `cercano_stats`

## Parameters

No parameters required.

## Output

Returns a markdown-formatted report including:

- **Total requests** processed locally
- **Local tokens** (input + output)
- **Cloud tokens** (if host-reported via `cercano_submit_usage`)
- **Kept local percentage** (when cloud data is available)
- **Estimated cloud tokens saved** (when no cloud data reported)
- **Breakdown by tool** (e.g. summarize, extract, classify)
- **Breakdown by model** (e.g. qwen3-coder, gemma3:4b)
- **Recent activity** (last 7 days)

## Examples

**Get usage stats:**
```json
{}
```

**Sample output:**
```
## Cercano Usage Statistics

**Total requests:** 42
**Local tokens:** 18,500 (15,200 in, 3,300 out)
**Estimated cloud tokens saved:** 18,500

### By Tool
- cercano_summarize: 20 calls, 12,000 tokens
- cercano_extract: 15 calls, 4,500 tokens
- cercano_classify: 7 calls, 2,000 tokens

### By Model
- qwen3-coder: 42 calls, 18,500 tokens

### Recent Activity
- 2026-03-24: 12 calls, 5,200 tokens
- 2026-03-23: 30 calls, 13,300 tokens
```
