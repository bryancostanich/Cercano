# Architecture Decision: Usage Telemetry & Token Savings Metrics

## Date: 2026-03-23

## Context
Cercano needs usage telemetry to answer: how much is it being used, and how many cloud tokens are being saved by running inference locally? This is especially important for the MCP co-processor use case.

## Decisions

### 1. Metrics Captured

**Per-request event:**

| Field | Type | Description |
|-------|------|-------------|
| `timestamp` | datetime | When the request occurred |
| `tool_name` | string | MCP tool invoked (e.g., `cercano_summarize`) |
| `model` | string | Ollama model used (e.g., `qwen3-coder`) |
| `input_tokens` | int | Tokens in the prompt sent to Ollama |
| `output_tokens` | int | Tokens in the response from Ollama |
| `duration_ms` | int | End-to-end latency |
| `was_escalated` | bool | Whether the request was escalated to cloud |
| `cloud_provider` | string | Cloud provider if escalated (empty otherwise) |

**Host-reported cloud usage (opt-in):**

| Field | Type | Description |
|-------|------|-------------|
| `timestamp` | datetime | When the report was received |
| `cloud_input_tokens` | int | Tokens sent to cloud model |
| `cloud_output_tokens` | int | Tokens received from cloud model |
| `cloud_provider` | string | e.g., `anthropic`, `google` |
| `cloud_model` | string | e.g., `claude-opus-4-6` |

### 2. Storage: SQLite

- **Location:** `~/.config/cercano/telemetry.db`
- **Why:** Structured queries for aggregation, single file, zero config, mature Go drivers, WAL mode for fast concurrent writes.
- **Tables:** `events` (local request events), `cloud_usage` (host-reported cloud events).

### 3. Aggregation

Three levels, all computed via SQL at query time:
- **Per-session** — since server start
- **Per-day** — grouped by date
- **Cumulative** — all-time totals

Rollups by tool name, model, and time period.

### 4. Token Savings Estimation

**Phase 1 (launch):** 1:1 mapping — every token processed locally is counted as a cloud token saved. Simple, directionally correct.

**Phase 2 (calibrate with real data):** Refine with actual cloud-vs-local token ratios from host-reported usage data. Different tasks may have different ratios.

When host-reported cloud usage is available, show actual local-vs-cloud comparison instead of estimates.

### 5. Host-Reported Cloud Usage

- Opt-in MCP tool: `cercano_report_usage`
- Hosts (Claude Code, Cursor, etc.) can call this to report their cloud token consumption
- Enables full picture: "X tokens local, Y tokens cloud, Z% kept local"
- Stored separately in `cloud_usage` table
- No host is required to report — local metrics work standalone

### 6. Privacy Boundaries

**Never recorded:**
- Prompt or response content
- File paths
- Conversation IDs
- API keys or credentials

**Recorded:**
- Tool names, model names, token counts, timestamps, latency
- Only aggregate counts, never content
