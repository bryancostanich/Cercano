# Track Plan: Usage Telemetry & Token Savings Metrics

## Phase 1: Design & Architecture [checkpoint: f5244b9]

### Objective
Define what to measure, how to store it, and how to surface metrics — with a focus on quantifying cloud token savings from local inference.

### Tasks
- [x] Task: Define the metrics to capture (tool invocations, input/output token counts, model used, latency, estimated cloud token equivalent).
- [x] Task: Decide storage strategy — SQLite at `~/.config/cercano/telemetry.db`, WAL mode.
- [x] Task: Decide aggregation approach — per-session, per-day, and cumulative, computed via SQL at query time.
- [x] Task: Design the token savings estimation model — 1:1 mapping at launch, calibrate with real data soon after.
- [x] Task: Design host-reported cloud usage ingestion — opt-in `cercano_report_usage` MCP tool for hosts to report cloud token usage.
- [x] Task: Decide privacy boundaries — never record prompt/response content, file paths, conversation IDs, or API keys.
- [x] Task: Write architecture decision document.
- [x] Task: Conductor - User Manual Verification 'Design & Architecture' (Protocol in workflow.md)

## Phase 2: Collection Layer [checkpoint: b4e51ff]

### Objective
Instrument the server to capture usage events at the right points without impacting latency.

### Tasks
- [x] Task: Define telemetry event struct and storage interface.
- [x] Task: Implement async event collection (fire-and-forget from request path).
- [x] Task: Instrument MCP tool handlers to emit events (tool name, model, token counts, duration).
- [-] Task: Instrument the SmartRouter/agent layer to capture routing decisions and escalation events. *(deferred — MCP handlers cover primary use case)*
- [x] Task: Implement token counting for Ollama requests/responses.
- [x] Task: Add `cercano_report_usage` MCP tool for host-side cloud token reporting (opt-in).
- [x] Task: Red/Green TDD for all collection components.
- [x] Task: Conductor - User Manual Verification 'Collection Layer' (Protocol in workflow.md)

## Phase 3: Storage & Aggregation [checkpoint: e1ddfc3]

### Objective
Persist events and compute aggregated metrics for reporting.

### Tasks
- [x] Task: Implement storage backend (per design decision in Phase 1). *(completed in Phase 2)*
- [x] Task: Implement aggregation queries (totals by tool, by model, by time period).
- [x] Task: Implement cloud token savings calculator (local tokens processed vs. estimated cloud equivalent).
- [x] Task: Red/Green TDD.
- [-] Task: Conductor - User Manual Verification 'Storage & Aggregation' *(deferred — will verify with Phase 4)*

## Phase 4: Reporting MCP Tool & Dashboard [checkpoint: 47b34ea]

### Objective
Expose metrics to users via an MCP tool and optional summary output.

### Tasks
- [x] Task: Add `cercano_stats` MCP tool (returns usage summary, token savings, top models/tools).
- [x] Task: Add stats to server startup log (cumulative usage since install).
- [x] Task: Add `--stats` CLI flag for quick terminal summary.
- [x] Task: Red/Green TDD.
- [x] Task: Update README.md with telemetry documentation.
- [x] Task: Conductor - User Manual Verification 'Reporting MCP Tool & Dashboard' (Protocol in workflow.md)

## Phase 5: Host Cloud Token Capture via Hook

### Objective
Automatically capture real cloud token usage from Claude Code's transcript file using a PostToolUse hook, replacing the 1:1 estimate with actual data.

### Design Notes
- Claude Code's transcript JSONL (`transcript_path`) already contains `usage` on every assistant message: `input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`
- Total cloud tokens per message = `input_tokens + cache_creation_input_tokens + cache_read_input_tokens + output_tokens` (volume) or `input_tokens + output_tokens` (billed, since cache reads are cheaper)
- A `PostToolUse` hook matching `mcp__cercano__.*` fires after every cercano call
- The hook receives `transcript_path` in its JSON input
- A script parses the JSONL, computes cumulative cloud tokens, calculates the delta since last report, and writes to Cercano's telemetry DB
- No new binaries, no OTel collector — just a shell/python script

### Tasks
- [x] Task: Write the hook script — parse transcript JSONL, extract cumulative cloud token usage, compute delta since last report, write to telemetry.db.
- [x] Task: Track last-reported position so the hook only processes new entries — uses session-keyed state file at `~/.config/cercano/hook_state.json`.
- [x] Task: Configure the PostToolUse hook in Claude Code settings — automated via `cercano setup` command.
- [x] Task: Update `cercano_stats` to show actual vs estimated cloud tokens when hook data is available. *(already implemented — shows "host-reported" vs "estimated" based on data presence)*
- [-] Task: Red/Green TDD for the hook script. *(Python script — tested manually with real transcript)*
- [x] Task: Update README.md with hook setup instructions.
- [ ] Task: Conductor - User Manual Verification 'Host Cloud Token Capture via Hook' (Protocol in workflow.md)

## Phase 6: Per-Session Usage Tracking

### Objective
Track telemetry per MCP session (each Claude Code window spawns its own `cercano --mcp` process) so users can see usage breakdowns by session, not just cumulative totals.

### Design Notes
- Each `cercano --mcp` process generates a UUID session ID at startup
- Session ID stored in a `sessions` table with start timestamp
- Every event row gets a `session_id` column (FK to sessions)
- `cercano_stats` gains a `by_session` breakdown showing per-window usage
- Sessions labeled by start time (e.g., "2026-03-25 14:32") since Claude Code doesn't expose a window name
- Schema migration adds `session_id` column with empty default for pre-existing rows

### Tasks
- [~] Task: Add `sessions` table and `session_id` column to events — schema migration in `migrateSchema()`.
- [~] Task: Generate UUID session ID on MCP server startup — pass to Server and Collector.
- [~] Task: Record session start — insert row into `sessions` table when MCP server initializes telemetry.
- [~] Task: Tag all emitted events with the session ID — update `Event` struct and `RecordEvent`.
- [~] Task: Add `BySession` stats query — aggregate events grouped by session_id, join with sessions for timestamps.
- [~] Task: Update `cercano_stats` output to include per-session breakdown.
- [~] Task: Red/Green TDD.
- [ ] Task: Conductor - User Manual Verification 'Per-Session Usage Tracking' (Protocol in workflow.md)
