# Usage Telemetry & Token Savings Metrics

## Overview
Cercano captures usage telemetry to answer two questions: how much is it being used, and how many cloud tokens are saved by running inference locally? This matters most for the MCP co-processor use case. The feature records per-request events, optionally ingests real cloud token usage reported by the host, aggregates everything via SQL, and surfaces metrics through an MCP tool, CLI flag, and startup log — all while strictly never recording prompt/response content.

## Design / Architecture
Storage is SQLite at `~/.config/cercano/telemetry.db` in WAL mode (structured aggregation queries, single file, zero config, fast concurrent writes). Two main tables: `events` (local request events) and `cloud_usage` (host-reported cloud events), plus a `sessions` table.

Local per-request events capture: timestamp, tool_name, model, input_tokens, output_tokens, duration_ms, was_escalated, and cloud_provider (if escalated). Host-reported cloud events capture: timestamp, cloud_input_tokens, cloud_output_tokens, cloud_provider, cloud_model.

Collection is **async / fire-and-forget** from the request path so it never impacts latency. MCP tool handlers emit events (tool name, model, token counts, duration); token counting is implemented for Ollama requests/responses. Aggregation is computed at query time via SQL at three levels — per-session (since server start), per-day, and cumulative all-time — with rollups by tool name, model, and time period.

Token-savings estimation launched with a 1:1 mapping (every token processed locally counted as a cloud token saved — simple and directionally correct), designed to calibrate later with actual cloud-vs-local ratios from host-reported data. When host-reported cloud usage is present, real local-vs-cloud comparison is shown instead of the estimate.

## Key behaviors / capabilities
- **`cercano_stats` MCP tool** — returns a usage summary, token savings, top models/tools, a per-session breakdown, and "host-reported" vs "estimated" cloud tokens depending on data presence.
- **`cercano_report_usage` MCP tool** — opt-in; hosts (Claude Code, Cursor, etc.) report their cloud token consumption, stored in `cloud_usage`. No host is required to report — local metrics work standalone.
- **CLI + startup** — a `--stats` CLI flag prints a quick terminal summary; cumulative usage since install is logged at server startup.
- **Automatic host cloud-token capture (hook)** — a `PostToolUse` hook matching `mcp__cercano__.*` fires after every Cercano call, receiving `transcript_path`. A script parses Claude Code's transcript JSONL (which carries `usage` per assistant message: input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens), computes cumulative cloud tokens, calculates the delta since the last report, and writes to `telemetry.db` — replacing the 1:1 estimate with actual data. No new binaries, no OTel collector. Last-reported position is tracked in a session-keyed state file at `~/.config/cercano/hook_state.json`. The hook is configured automatically by `cercano setup`.
- **Per-session tracking** — each `cercano --mcp` process (one per Claude Code window) generates a UUID session ID at startup, recorded in the `sessions` table; every event row is tagged with `session_id`. A `BySession` query joins events with sessions for timestamps; sessions are labeled by start time since Claude Code exposes no window name. A schema migration in `migrateSchema()` adds the `session_id` column with an empty default for pre-existing rows.

## Notable decisions / constraints
- **Privacy boundaries (hard):** never records prompt or response content, file paths, conversation IDs, or API keys/credentials. Records only tool names, model names, token counts, timestamps, and latency — aggregate counts only.
- Total cloud tokens per message can be computed two ways: volume (`input + cache_creation + cache_read + output`) or billed (`input + output`, since cache reads are cheaper).
- Instrumenting the SmartRouter/agent layer for routing/escalation events was deferred — MCP handlers cover the primary use case.
- The Python hook script was tested manually against a real transcript rather than via Red/Green TDD.
