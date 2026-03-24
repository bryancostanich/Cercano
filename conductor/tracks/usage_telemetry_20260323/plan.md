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

## Phase 2: Collection Layer

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
- [ ] Task: Conductor - User Manual Verification 'Collection Layer' (Protocol in workflow.md)

## Phase 3: Storage & Aggregation

### Objective
Persist events and compute aggregated metrics for reporting.

### Tasks
- [ ] Task: Implement storage backend (per design decision in Phase 1).
- [ ] Task: Implement aggregation queries (totals by tool, by model, by time period).
- [ ] Task: Implement cloud token savings calculator (local tokens processed vs. estimated cloud equivalent).
- [ ] Task: Red/Green TDD.
- [ ] Task: Conductor - User Manual Verification 'Storage & Aggregation' (Protocol in workflow.md)

## Phase 4: Reporting MCP Tool & Dashboard

### Objective
Expose metrics to users via an MCP tool and optional summary output.

### Tasks
- [ ] Task: Add `cercano_stats` MCP tool (returns usage summary, token savings, top models/tools).
- [ ] Task: Add stats to server startup log (cumulative usage since install).
- [ ] Task: Add `--stats` CLI flag for quick terminal summary.
- [ ] Task: Red/Green TDD.
- [ ] Task: Update README.md with telemetry documentation.
- [ ] Task: Conductor - User Manual Verification 'Reporting MCP Tool & Dashboard' (Protocol in workflow.md)
