# Accurate Token Accounting and Config Metrics

## Problem and motivation

Cercano needs trustworthy token accounting across its inference paths and a config page that explains usage over time. Existing tool events, external cloud reports, and request-context estimates represent different measurements. Combining them would risk double counting and false precision.

The preceding database audit reported 59 tool events and 68 external cloud reports, ending July 20 despite sessions continuing later. Historical records have incomplete attribution and uncertain measurement quality. These findings motivate investigation of current collection coverage; they do not establish the cause of the gap.

The existing config tab list has no Token Metrics tab. The worker RequestAccounting message carries context estimates and output reserves, not actual consumed tokens.

## Goals

Record actual inference attempts across main-agent calls, delegation, local inference, research, vision inspection, compaction, retries, and fallback calls. Each physical attempt must have a stable identity, attribution, outcome, and an explicit indication of usage completeness. Operations may group attempts but must not count their usage again. Externally reported usage remains a separate record kind and must not be added to internally measured usage without evidence that the populations are disjoint.

Make accounting asynchronous. The inference path may construct a small immutable event and hand it off without waiting for persistence, database availability, aggregation, or a worker-to-server accounting acknowledgment. Significant processing belongs in background workers. Persistence and delivery should be robust, retryable, and duplicate-safe.

Add a Token Metrics config tab with date filtering, usage totals, time-series graphs, provider/model breakdowns, and accounting health. Begin trustworthy reporting at cutover, without migrating historical counters into the new reporting model.

## Constraints and invariants

Inference must not block or fail because accounting storage is slow or unavailable. Background work handles transport where necessary, batched writes, retries, and health updates. Accounting resource use must be bounded. Queue saturation cannot silently discard usage or apply backpressure to inference: any unrecoverable loss must be counted and surfaced. Exact queue capacities and retry timings belong in the implementation plan and its validation, not arbitrary promises in this spec.

Delivery retries must not duplicate attempts or usage. Streaming updates must not be summed as independent complete calls. Failed attempts, retries, cancellations, and fallbacks remain distinct attempts, with unavailable usage distinct from a provider-reported zero. Preserve returned usage even when the attempt fails.

Provider/model attribution must reflect the actual serving route when known. Reporting host names are not model identities. Missing attribution stays unknown rather than being inferred from a tool label.

Input and output totals must have consistent documented semantics across adapters. Cache-read, cache-write, and reasoning counts are additional categories where reported; categories that are subsets of input or output must not be added again. Estimated context size, output reserves, avoided-content estimates, and token-saving counters are not consumed-token usage. A provider not reporting a category is different from reporting zero.

Persist timestamps in UTC and query calendar ranges using the viewer's timezone. Use calendar arithmetic across daylight-saving boundaries. Aggregate queries and graph preparation stay outside inference execution.

Retain existing historical tables and rows unchanged by this effort's cutover. New reporting must not blend them into new totals. Synthetic fixtures are for development and tests only, never real accounting history.

Orderly shutdown drains pending accounting within a bounded shutdown budget and reports failures. Non-blocking inference cannot guarantee durable observation of every event before a hard crash. Provider cancellation may also prevent final usage from arriving. Minimize these gaps and represent known incompleteness honestly; do not claim billing-grade completeness.

## Decisions

### Record boundaries and storage

The approved direction is separate inference-attempt, operation, and external-report record types within the existing telemetry database. A parallel ledger duplicating the same measurements would not fix collection boundaries or double counting. The implementation must map current writers before choosing interception points, retaining one owner per measured attempt. Existing worker context accounting is not a substitute for usage reporting.

### Historical data

Chosen: preserve legacy tables without migration or inclusion in new totals. The audit demonstrated that migration is possible but not worth its implementation and interpretation cost for this sparse, uncertain history.

| Axis | Preserve legacy tables, start fresh | Convert historical records for new graphs |
|---|---|---|
| Historical graphs | New data from cutover only | Earlier observations available with caveats |
| Accuracy | Clean reporting boundary | Requires separate provenance and uncertainty treatment |
| Work and risk | Avoid conversion and deduplication rules | Timestamp conversion, attribution, and migration validation |
| Rationale | Approved; prioritize future collection | Rejected as insufficient value for this dataset |

### Reliability and asynchronous execution

Chosen: inference continues independently of accounting, with robust background recording and visible degraded health. The user explicitly requires that actual accounting work be offloaded, not merely that errors be ignored.

| Axis | Asynchronous recording, never block inference | Require successful accounting persistence |
|---|---|---|
| Inference path | Small event handoff only | Waits for durable acknowledgment |
| Storage failure | Background retry and visible degradation | Can prevent inference |
| Completeness | Crash windows and overload must be disclosed | Stronger start-record durability, still no guarantee of final provider usage |
| Rationale | Approved; availability is mandatory | Incompatible with the user's non-blocking requirement |

Accounting health must expose pending work, persistence failures, retries, last successful persistence, and known unrecoverable loss. Persistent failures must be visible in config, not just logs. Missing instrumentation, silent overflow, and duplicate accounting are defects, not normal operating assumptions.

### Date interpretation

Chosen: calendar days in the viewer's timezone. Today starts at local midnight. Past 7 days means today and the preceding six dates; past 30 days means today and the preceding 29 dates. All time starts at accounting cutover. Show timezone and the partial current day.

| Axis | Calendar days | Rolling elapsed-time windows |
|---|---|---|
| Daily graphs | Align with displayed dates | Usually have partial first and last days |
| Boundaries | Timezone-aware calendar arithmetic | Fixed elapsed durations |
| Testing | Daylight-saving and midnight transitions | Duration boundaries plus bucket timezone handling |
| Rationale | Approved; intuitive daily reporting | More suitable for precise elapsed-time comparisons |

## Proposed first-version page scope

The draft scope is a Token Metrics tab alongside the existing config tabs. Provide Today, Past 7 days, Past 30 days, All time, and a custom date range. Filter by provider, model, and source of work. Display input, output, total tokens, and inference-attempt count, with cache and reasoning breakdowns where supported. Include simple usage-over-time graphs and provider/model breakdowns.

Keep external reports distinguishable from internally measured inference. Any summary must clearly state its population rather than present a combined total of potentially overlapping sources. Include explicit unknown attribution and incomplete-usage counts.

Show tracking start date, accounting health, and pending persistence. Empty buckets mean no recorded usage; known collection gaps must not appear as confidently measured zeros. Reporting can lag inference briefly because persistence is asynchronous.

This page scope is proposed for spec approval. Exact layout, query interfaces, and delivery implementation will be specified in the execution plan after source inspection.

## Non-goals

No historical migration, synthetic production history, general-purpose observability platform, pricing catalog, dollar-cost estimates, billing enforcement, or changes to provider selection. No claim that external host reports can reconstruct individual internal attempts. No conflation of saved-token estimates with measured consumption.

## Acceptance and verification

Demonstrate collection coverage with an explicit inventory of inference paths and their accounting owner. Investigate the reported gap between session activity and usage collection using current source and focused probes; do not assume the historical gap's cause.

Test provider usage normalization for streamed and non-streamed responses, missing categories, cache/reasoning subset semantics, errors with usage, cancellations, retries, and fallback attribution. Test duplicate delivery and cumulative stream updates without double counting.

Exercise database contention, failed writes, queue saturation, worker disconnects, recovery, and bounded shutdown. Verify that storage failures and pending delivery cannot stall inference and that degradation or loss is observable. Validate normal-load recording reliability with analytically justified load assumptions before any benchmark or sweep.

Test fresh and existing databases, preservation of legacy rows, cutover isolation, date boundaries, daylight-saving transitions, unknown attribution, incomplete records, and indexed aggregate queries. Use integration tests for worker/server transport and the metrics query interface, plus focused config-navigation/render tests. Synthetic fixtures must be deterministic and isolated from user databases.

## Status

Approved by the user in conversation. The execution plan is captured separately in plan.md and requires its own approval before implementation. Detailed transport verification, writer ownership, adapter normalization, and query-interface work are phased in that plan; any newly discovered architectural choice must return for approval before implementation.
