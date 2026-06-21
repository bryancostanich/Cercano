# Cloud Token Savings Estimation

> Status: Planned — not yet built. Migrated from `conductor/tracks/savings_estimation_20260326/`.

## Overview / Goal

Estimate the actual cloud tokens saved by measuring the content Cercano kept out of the cloud context, minus the overhead of using Cercano.

### Problem

The current "% local" metric compares local tokens processed vs cloud tokens consumed. This is misleading:

- A `cercano_summarize` call on a 500-line file processes ~4,000 local tokens and returns a ~100 token summary. The cloud spends ~200 tokens on the tool call round-trip. But without Cercano, the cloud would have spent ~4,000 tokens reading the file + ~500 tokens reasoning about it. The **actual savings** are ~4,300 tokens, not "4,000 local tokens."
- The overhead of using Cercano (tool call formatting, reading the response) eats into savings but isn't tracked.
- `cercano_document` is the most extreme case — the cloud never sees the file at all, potentially saving thousands of tokens per call.

## Design / Approach: Content-Avoided Tracking

### Core Metric: `content_tokens_avoided`

For each tool call, measure the **input content size** — the tokens the cloud would have had to process if Cercano didn't handle it locally. This is the raw content that never entered the cloud context.

### Per-Tool Estimation

| Tool | What cloud avoids | How to measure |
|------|------------------|----------------|
| `cercano_summarize` | Reading the full file/text | Input content token count |
| `cercano_extract` | Reading the full file/text | Input content token count |
| `cercano_classify` | Reading the full file/text | Input content token count |
| `cercano_explain` | Reading the full file/text | Input content token count |
| `cercano_document` | Reading + writing the file | Input content × 2 (read + write cycle) |
| `cercano_research` | WebSearch + WebFetch HTML dumps | Total fetched page content token count |
| `cercano_fetch` | WebFetch raw HTML | Fetched content token count |
| `cercano_local` | Varies | Input content token count (prompt + context) |

### Overhead Estimation

Each Cercano call costs the cloud:
- **Request overhead**: ~50 tokens (tool name, parameters, JSON formatting)
- **Response tokens**: actual response size (already tracked as output tokens)

So: `net_savings = content_tokens_avoided - response_tokens - request_overhead`

### Token Counting

Use a simple heuristic: **1 token ≈ 4 characters** (standard GPT/Claude approximation). No need for a real tokenizer — this is an estimate, not an exact count.

### Data Model Changes

**Telemetry Event Extension** — add to the existing `Event` struct:
```go
ContentTokensAvoided int  // tokens the cloud didn't have to process
ResponseTokens       int  // tokens in the response the cloud DID process (already tracked as output tokens)
```
`ContentTokensAvoided` is set by each MCP handler based on the input content size.

**Stats Extension** — add to `UsageStats`:
```go
TotalContentAvoided   int  // sum of content_tokens_avoided across all events
TotalOverhead         int  // estimated cloud overhead (calls × 50 + total response tokens)
EstimatedNetSavings   int  // TotalContentAvoided - TotalOverhead
```

### Dashboard Changes

Add a savings section to the stats dashboard:

```
┌ Estimated Cloud Savings ──────────────────────────────────
  Content kept out of cloud:   142,000 tokens
  Cercano overhead (responses): -12,000 tokens
  Cercano overhead (calls):       -800 tokens (16 calls × 50)
  ─────────────────────────────────────────
  Estimated net savings:       129,200 tokens
```

### Where Content Size is Measured

Each MCP handler already reads the input content. The measurement happens at the same point:

- **File-based tools** (summarize, extract, classify, explain): `len(fileContent) / 4` after reading the file
- **Text-based tools**: `len(args.Text) / 4`
- **Document tool**: `len(fileContent) / 4 * 2` (read + write avoidance)
- **Research tool**: sum of fetched page sizes / 4
- **Fetch tool**: `len(fetchedContent) / 4`

The measurement is done in the handler, passed to `emitEvent` as `contentTokensAvoided`.

### Non-Goals

- Exact tokenization (heuristic is fine for estimation)
- Per-model token counting differences
- Tracking what the cloud "would have done" with the content (just measure what it didn't see)
- Retroactive estimation for existing telemetry data (new metric starts from implementation)

## Plan / Tasks

### Phase 1: Data Model & Storage

Objective: Extend telemetry to store content-avoided metrics per event.

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

### Phase 2: MCP Handler Instrumentation

Objective: Measure input content size in each handler and pass it to telemetry.

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

### Phase 3: Dashboard Update

Objective: Show estimated savings in the stats dashboard.

- [ ] Task: Add savings section to `FormatStats`.
    - [ ] Show content kept out of cloud, overhead, and net savings.
    - [ ] Only show section when content_tokens_avoided > 0.
    - [ ] Red/Green TDD: TestFormatStats_IncludesSavings.
- [ ] Task: Add per-tool savings to the "By Tool" section.
    - [ ] Show content avoided alongside existing token counts.
- [ ] Task: Conductor - User Manual Verification 'Dashboard Update' (Protocol in workflow.md)
