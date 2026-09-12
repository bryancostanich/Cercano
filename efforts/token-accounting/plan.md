# Accurate Token Accounting and Config Metrics

Execute the approved spec.md. Preserve legacy history without migration; all new reporting starts at a persisted cutover. Accounting must be asynchronous and must never wait on storage or transport on the inference path. The user approved the spec, execution plan, and autonomous execution. Implementation is underway. The user subsequently approved extending the existing usage/sink/collector/worker transport architecture, adding wiring only for demonstrated coverage gaps; this supersedes the provisional constructor-dependency decision. If delegation fails, continue directly as explicitly requested.

Paths below are relative to the repository. Use an isolated worktree for implementation. Delegate bounded editing and test tasks when execution tools are available; do not treat unverified sub-agent summaries as evidence. Checkpoint each completed phase with explicit paths and a conventional-commit subject/body. Never push. If tool availability prevents required execution, report the limitation rather than claiming completion.

## Phase 1 — Confirm collection boundaries and reproduce gaps

Execution evidence so far: isolated worktree `/Users/bryancostanich/git_repos/bryan_costanich/Cercano-token-accounting`, branch `feature/token-accounting`, based on `9d86a7ed`. Baseline `go test ./internal/usage ./internal/telemetry` from `source/server` passed. This is baseline verification only, not a regression probe or evidence that the planned behavior exists. Write-capable delegation failed validation twice during setup/investigation because the delegate did not execute any granted write/execute tools; no delegated implementation claims have been accepted. Provider coverage mapping and transport verification remain incomplete.

Direct regression evidence: `go test ./internal/usage ./internal/telemetry -run TestLegacyAccounting -v -count=1` proved (1) failed Chat drops returned 11/7 counts, (2) stream error counts are discarded and only Close emits zero, (3) a blocked writer plus one-slot queue persists two of three emissions without a loss counter, and (4) startup-scoped defers leave one session and zero post-startup events. Production startup had precisely this premature-close pattern. Extracted `startAgentTelemetry` reproduced zero persisted events in a failing command-package regression, then moving closure to server shutdown made it pass. `go test ./cmd/cercano ./internal/usage ./internal/telemetry` passes. This proves a current lifecycle defect, not the historical cause of every missing record. Lifetime repair pulled forward from Phase 4 as the user-approved first repair; bounded shutdown remains Phase 3 work. No production database was opened.

Coverage findings: main in-process uses `usage.Wrap`; worker main sends whole-turn totals from host `RequestAccounting` handling rather than attempt observations. Dispatch uses `RecordUsage` gating and successful response summaries in `dispatch.Engine`; worker dispatch has a separate construction path. MCP research calls `ProcessRequest` through `grpcModelCallerWithTokens` and emits additional tool summaries; those are operation observations, not extra model attempts. Vision calls its provider directly. Recap/local compaction call `openProvider.Process`; cloud compaction calls its cloud provider directly. Profile-chain and resilience wrappers retry inner providers, and Anthropic/OpenAI/Responses adapters also retry internally. Bedrock also retains AWS SDK default retry behavior, requiring SDK retry-attempt visibility rather than treating a Converse call as a physical attempt. Physical-attempt interception must be inside all retry layers; provider wrappers alone cannot count internal retries. Existing legacy event and cloud-report rows must not be unioned into new attempt totals.

Transport finding: worker `sender.send` blocks on a shared channel and `sender.run` serializes stream writes. Accounting cannot call this from inference; background delivery also needs bounded scheduling relative to control messages. A one-frame lower-priority queue on the same sender now demonstrates bounded accounting admission without transport waits, and queued control priority, under an intentionally blocked stream. `go test -race ./internal/worker -run TestAccountingAdmission -count=20` and `go test ./internal/worker` pass. This proves scheduling feasibility, not reliable delivery: frame-size caps, bounded pending observations, acknowledgments, host-side ack scheduling, disconnect health, and draining before TurnDone remain Phase 4 obligations. No new transport or durable spool is authorized.

Objective: establish one recording owner for each actual attempt and distinguish source-confirmed defects from historical hypotheses before changing behavior. Files: source/server/internal/usage/recording_provider.go; internal/server/server.go and usage_sink.go; internal/dispatch/engine.go; internal/inference/profilechain and resilience; internal/worker/worker.go, host.go, wire.go and worker_dispatch.go; internal/mcp/server.go; telemetry/telemetry.go, all under source/server unless otherwise stated. Tests: small fake-provider and fake-store probes covering current missing/error behavior and worker delivery. No live billed calls required.

- [x] Trace provider construction and every inference path, including adapters with internal retries, main, dispatch, local tools, research, vision, and compaction; record a path-to-recording-owner coverage checklist in this plan.
- [x] Trace current usage sinks and tool-event writers; identify which are aggregate observations and which duplicate lower-level usage.
- [x] Reproduce silent collector overflow and missing failure/early-stream usage with controlled probes before applying fixes.
- [x] Trace server and worker startup wiring to investigate missing recent accounting; document what is proven and what historical evidence cannot establish.
- [x] Identify interception points inside retry/fallback boundaries so physical attempts are separate, without counting wrappers twice.
- [x] Confirm existing worker transport can carry background usage delivery without blocking inference or competing unboundedly with control messages. Stop for a human decision if a new transport or durable spool is required; neither is silently authorized by this plan.

## Phase 2 — Define records and normalize provider usage

Objective: make consumed-token semantics explicit before persistence or graphs. Files: source/server/internal/usage, internal/llm/provider.go, stream.go, collect.go, and adapters in anthropic, bedrock, openai, responses, ollama; inference/profilechain and resilience as needed. Tests: adapter fixtures and fake-provider lifecycle tests, including error responses with usage.

- [~] Introduce stable attempt identity, operation/conversation/session correlation, source-of-work attribution, actual provider/model route, UTC times, lifecycle outcome, and explicit usage availability/completeness.
- [ ] Define normalized input/output totals and optional cache-read, cache-write, and reasoning categories; document each adapter's subset/additive semantics and retain unknown rather than zero where absent.
- [ ] Extend response and stream envelopes compatibly; preserve partial usage through stream collection and errors.
- [ ] Record attempt start and terminal observations for non-streaming and streaming calls. Finalize on terminal stream signals or errors as well as Close; make repeated Close and terminal delivery idempotent.
- [ ] Keep stream usage snapshots cumulative per attempt rather than summing repeated snapshots. Preserve legitimate zero values with presence metadata.
- [ ] Record retries and fallback attempts separately with actual serving route; keep aggregate operation events separate.
- [ ] Verify context estimates, output reserves, avoided tokens, and legacy token-saving fields never enter consumed-token totals.

## Phase 3 — Add storage and non-blocking background collection

Objective: reliable additive storage in telemetry.db with a bounded, non-blocking event handoff. Files: source/server/internal/telemetry (split new accounting storage/collector files rather than enlarging the legacy implementation unnecessarily), internal/usage, server startup/shutdown ownership. Tests: temporary SQLite databases, failing stores, deterministic queue saturation and concurrency tests.

- [ ] Add versioned additive schema for inference attempts, operations, external reports, cutover metadata, and accounting-health state. Do not rewrite or import legacy rows.
- [ ] Add uniqueness for stable identities and monotonic observation revisions; apply lifecycle updates idempotently and prevent late partial observations from replacing final records.
- [ ] Store timestamps in a consistent sortable UTC representation; add timestamp-range and useful attribution indexes, validated against intended queries.
- [ ] Implement bounded event admission with no database, transport acknowledgment, disk serialization, or unbounded goroutine creation on inference paths.
- [ ] Implement background batched persistence and bounded retry/backoff for temporary failures; do not hold admission locks across storage operations.
- [ ] Derive queue/batch/retry defaults from documented event-rate and memory assumptions. Calculate expected capacity before any load benchmark, then verify the assumptions.
- [ ] Track accepted, pending, persisted, retried, failed, and lost observations, oldest pending age, and last successful persistence. Keep health counters independent of the saturated event queue; persist gap information when storage recovers.
- [ ] Make overflow immediately observable without blocking inference. Treat sustained overload as degraded health, not successful collection.
- [ ] Implement bounded shutdown drain and restart treatment of unfinished persisted attempts; never relabel unrecorded final usage as zero.
- [ ] Test database locks, unavailable storage, retry success, repeated delivery, out-of-order updates, queue saturation, and shutdown races. Use race tests on touched concurrent packages.

## Phase 4 — Connect server, workers, and all inference paths

Objective: complete the coverage checklist through existing process boundaries, with background delivery and no double counting. Files: source/server/internal/worker/wire.go, worker.go, host.go, worker_dispatch.go; internal/runner; internal/server/server.go and usage_sink.go; internal/dispatch/engine.go; internal/mcp/server.go; inference provider construction and the paths identified in Phase 1. Tests: worker/server integration with fake inference and storage.

- [ ] Add usage-observation messages distinct from RequestAccounting context estimates; perform serialization and transport writes from background delivery workers.
- [ ] Give observations stable worker/incarnation identities and revisions; retry delivery safely and acknowledge persistence off the inference path, retaining bounded pending state until acknowledged.
- [ ] Wire the same non-blocking sink contract into in-process and worker-owned providers at the recording boundaries established in Phase 1.
- [ ] Separate tool operation summaries from attempt usage; retire duplicate writes into new accounting while preserving legacy read compatibility.
- [ ] Adapt external-report ingestion to its separate record type without treating host labels as model identities. Preserve unknown completeness when the external source cannot supply stable report identity.
- [ ] Propagate known delivery degradation and worker-disconnect uncertainty to server health. Explain the unrecoverable hard-crash window rather than claiming exact loss counts when unknowable.
- [ ] Order shutdown so producers stop, worker pending delivery drains, and server persistence closes last, within a bounded budget.
- [ ] Verify main/local/cloud, dispatch, research, vision, compaction, retry, and fallback coverage using the Phase 1 checklist.
- [ ] Test duplicate transport messages, acknowledgment loss, worker disconnect, cancellation, worker termination, storage stalls, and inference progress under those conditions. Confirm one persisted record per physical attempt.

## Phase 5 — Expose indexed metrics and health queries

Objective: provide a bounded read interface for config, independent of inference execution. Files: source/proto/agent.proto and generated bindings using source/proto/generate.sh; source/server/internal/server handlers and telemetry query code; client interface/adapters discovered from existing config/context RPC patterns. Tests: storage query fixtures and client/server interface integration.

- [ ] Add a metrics query interface with half-open UTC bounds, viewer timezone, provider/model/source filters, and explicit record population. Return totals, buckets, breakdowns, completeness counts, cutover, and health.
- [ ] Use internally measured attempts for the primary summary; expose external reports separately, never union them into primary token totals.
- [ ] Implement calendar presets: Today, today plus preceding six or 29 dates, All time since cutover, and inclusive user-entered custom dates converted to exclusive next-midnight end bounds.
- [ ] Handle daylight-saving changes with calendar arithmetic. Validate timezones and bounds, cap query payload/bucket counts, and keep unknown attribution selectable.
- [ ] Ensure filters apply consistently to totals, buckets, and breakdowns; label partial current-day and incomplete/gap periods.
- [ ] Validate indexes with representative query plans on deterministic temporary fixtures; avoid per-row RPCs or loading complete inference history into the UI.
- [ ] Test midnight/daylight-saving boundaries, empty ranges, unknown metrics, legacy isolation, malformed filters, and totals equal to bucket sums for the same population.
- [ ] Regenerate bindings and run integration tests for the changed interface; avoid unrelated generated churn.

## Phase 6 — Build the Token Metrics config tab

Objective: usable terminal reporting with responsive asynchronous queries. Files: source/clients/cli/internal/ui/config_tabs.go, config_surface.go, new metrics page and tests; client interface/mocks touched by Phase 5. Tests: config navigation, rendering, filter state, stale response handling, and interface-backed page tests.

- [ ] Add the tab through the existing contentPage/config-surface conventions; update cycling, clamping, keyboard navigation, sizing, and tab tests.
- [ ] Implement date presets/custom range and provider/model/source filters, with visible timezone and tracking-since date.
- [ ] Display input, output, total tokens, attempt count, optional cache/reasoning categories, and explicit incomplete/unknown counts.
- [ ] Render simple usage-over-time and provider/model breakdown graphs within terminal width; keep external reports visibly separate.
- [ ] Query asynchronously through UI commands, reject stale responses after filter changes, and stop refresh scheduling when the page closes. No aggregation in the inference loop.
- [ ] Display loading, empty, query-error, pending-persistence, degraded, and known-gap states without claiming unrecorded periods are measured zeros.
- [ ] Test narrow terminals, focus navigation, rapid filter changes, page close/reopen, partial current-day labels, health recovery, and absence of old data in new totals.

## Phase 7 — Verify cutover and document operation

Objective: demonstrate end-to-end accounting correctness for the changed interfaces and document limitations without broad unrelated testing. Files: focused integration tests, docs/agent/README.md, docs/agent/self-dev.md, docs/features/cli/README.md as appropriate; update this plan with actual evidence and checkpoint identifiers.

- [ ] Run targeted Go tests for touched packages and concurrency race tests, then worker/server and metrics-interface integration tests. Record commands and outcomes; do not substitute source inspection for execution.
- [ ] Use deterministic test-only history to verify page totals against known expected records, including retries and separate external reports. Never seed the user's database.
- [ ] Verify an existing database retains legacy data, establishes cutover once, and does not reset it on restart. Verify clean install and interrupted initialization behavior.
- [ ] Verify background persistence under analytically specified normal and burst load, plus storage-failure injection; report measured handoff overhead and accounting health instead of unsupported reliability claims.
- [ ] Document token definitions, reporting populations, date semantics, tracking start, queue/health diagnostics, shutdown behavior, and hard-crash/provider-usage limitations.
- [ ] Review the final coverage checklist and disclose any unsupported path or missing provider category; stop rather than claim complete coverage with known uninstrumented paths.
- [ ] Checkpoint completed work with explicit paths and provide a final summary of changes, verification evidence, remaining limitations, and commits. Never push without an explicit request.


### Phase 2 implementation evidence

Added value-only, presence-aware `llm.TokenUsage` alongside unchanged legacy context counters and revisioned `usage.AttemptObservation` with context-carried sink/attribution. Attempt lifecycle helpers preserve counts on errors and early close; concurrent finalization and repeated Close emit one terminal observation. `CollectStream` now merges normalized snapshots before checking errors; its regression first failed with missing 11/7 usage, then passed. These are primitives, not yet production sink wiring or physical-attempt coverage.

`go test -race ./internal/llm ./internal/usage` and `go test ./internal/llm/... ./internal/inference/...` passed before adapter wiring. Anthropic Chat and stream normalization now preserve field presence, cumulative snapshots, cache breakdowns, and reasoning. Input total requires all additive input components to be known: fixture 11 uncached + 3 cache-read + 5 cache-write = 19 input, without adding breakdowns again. Missing cache fields deliberately leave total unknown. `go test ./internal/llm/anthropic ./internal/llm ./internal/usage` passes with synthetic HTTP/SSE tests. OpenAI's current SDK loses per-field presence in integer usage fields; its raw-response preservation still needs investigation before normalization can be honest. Adapter delegation again failed validation without edits; continuing directly.


OpenAI presence blocker verified: `go test ./internal/llm/openai -run TestSDKUsagePresenceProbe -v -count=1` passes, demonstrating Chat missing `usage` decodes identically to reported all-zero usage, and stream missing `completion_tokens` decodes identically to reported zero. `ClientConfig` exposes HTTPDoer but no public response-decoder hook; Chat responses expose headers, not raw response JSON. Correct field-presence recovery therefore requires intervening before SDK decoding. A maintained SDK patch or provider-client migration broadens dependency ownership/compatibility work; a second raw-body decoder adds parallel parsing and memory/performance risks on the inference path. Do not label ambiguous counters known. Escalate dependency/client scope before implementing that replacement; existing normalized records and Anthropic slice are checkpointed as `8e5d30089b44`.
