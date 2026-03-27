# Track Plan: Cloud Token Savings Estimation

## Phase 1: Data Model & Storage

### Objective
Extend telemetry to store content-avoided metrics per event.

### Tasks
- [ ] Task: Add `content_tokens_avoided` column to the telemetry events table.
    - [ ] Add column to SQLite schema (with migration for existing DBs).
    - [ ] Update `Event` struct with `ContentTokensAvoided int`.
    - [ ] Update `Insert` to write the new field.
    - [ ] Red/Green TDD: TestEvent_ContentTokensAvoided.
- [ ] Task: Add savings aggregation to `GetStats`.
    - [ ] Add `TotalContentAvoided`, `EstimatedNetSavings` to `UsageStats`.
    - [ ] Query: `SUM(content_tokens_avoided)` and compute net savings.
    - [ ] Red/Green TDD: TestGetStats_IncludesSavings.
- [ ] Task: Add per-tool savings to `ByTool` stats.
    - [ ] Add `ContentAvoided int` to `GroupedStat`.
    - [ ] Red/Green TDD: TestByTool_IncludesContentAvoided.

## Phase 2: MCP Handler Instrumentation

### Objective
Measure input content size in each handler and pass it to telemetry.

### Tasks
- [ ] Task: Add `EstimateTokens(content string) int` helper to the MCP package.
    - [ ] Simple `len(content) / 4` heuristic.
    - [ ] Red/Green TDD: TestEstimateTokens.
- [ ] Task: Update `emitEvent` signature to accept `contentTokensAvoided int`.
- [ ] Task: Instrument file-based handlers (summarize, extract, classify, explain).
    - [ ] After reading file/text content, compute `EstimateTokens(content)`.
    - [ ] Pass to `emitEvent`.
- [ ] Task: Instrument `cercano_document` handler.
    - [ ] Compute `EstimateTokens(fileContent) * 2` (read + write avoidance).
    - [ ] Pass to `emitEvent`.
- [ ] Task: Instrument `cercano_research` handler.
    - [ ] Sum fetched page content sizes.
    - [ ] Pass total to `emitEvent`.
- [ ] Task: Instrument `cercano_fetch` handler.
    - [ ] Compute `EstimateTokens(fetchedContent)`.
    - [ ] Pass to `emitEvent`.
- [ ] Task: Instrument `cercano_local` handler.
    - [ ] Compute `EstimateTokens(prompt + context)`.
    - [ ] Pass to `emitEvent`.

## Phase 3: Dashboard Update

### Objective
Show estimated savings in the stats dashboard.

### Tasks
- [ ] Task: Add savings section to `FormatStats`.
    - [ ] Show content kept out of cloud, overhead, and net savings.
    - [ ] Only show section when content_tokens_avoided > 0.
    - [ ] Red/Green TDD: TestFormatStats_IncludesSavings.
- [ ] Task: Add per-tool savings to the "By Tool" section.
    - [ ] Show content avoided alongside existing token counts.
- [ ] Task: Conductor - User Manual Verification 'Dashboard Update' (Protocol in workflow.md)
