# Dispatch correctness: tool contracts, compaction and progress

Implemented directly after inspecting dispatch `2a3752b5a4b7b6c2e222a222`, which spent roughly 53 minutes inspecting without editing/testing. No live-model experiment or change to model sampling was made.

## Confirmed defects and regressions

1. `Read` ignored unsupported `offset`/`limit` fields and returned the wrong file region. The audit also reproduced `Write` erasing a temporary fixture when `replacement` was supplied instead of required `content`. Shared schema validation now runs before side effects for Read, Write, Edit, LS, StatFile, Glob, Grep, rm_file and RunCommand. Unknown fields, required fields, types and declared bounds are enforced. Read additionally rejects reversed ranges. Explicit empty Write content remains valid. Other capability families and external MCP tools were not globally rewritten. Errors identify invalid fields without echoing argument values.
2. Segment boundaries separated a completed parallel tool exchange into three independent summaries. A separate probe froze an assistant call while its result remained in recent history, causing pairing repair to hide evidence. Segmenting, verbatim cutoffs, capped passes and partial-pass recovery now preserve complete exchanges, as well as existing timestamp constraints. Local summary chunking defers an oversized exchange instead of separating invocation from evidence. Pending exchanges remain live.
3. Dispatch construction consulted the unrelated local chat model for its retained-summary budget. Factories no longer probe that model; each pass receives the executing route's known window from the tool loop. The shared percentage/floor formula sizes the retained summary; unknown windows retain a conservative fallback and do not inherit another route's capacity. **Correction to the initial diagnosis:** the 40k activation threshold is a separate configured setting, not derived from the local window. It and segment cadence are preserved. This change does not claim to eliminate frequent compaction.
4. A summarizer fixture reporting 68 tokens was charged 1,456 estimated tokens. Production summarizer calls now carry usage accounting through a context-scoped meter, including known zero and normalized cache-inclusive counters. Missing input/output usage is estimated separately and labeled. No-call routing/cancellation failures do not fabricate usage. Legacy summarizer seams remain explicitly estimated. Telemetry and dispatch evidence retain attribution; error text describes token volume, not currency billed. Internal provider retries whose usage is not exposed at the summarizer boundary remain outside exact accounting; this is not an invoice reconstruction.
5. Budget stops previously returned no useful final handoff and could issue another model request after compaction alone exhausted the budget. Stops now return a bounded, deterministic ledger of actually executed actions, errors, recent inspections, and requests not executed at the crossing. The ledger survives model-history compaction. The parent receives the error classification, partial text and a transcript reference only when persistence was available. No automatic resumption or success claim is introduced.

## Conservative progress intervention

Agentic dispatches monitor exact repeated inspection arguments and unchanged model-visible results. JSON key order is canonicalized; tool-result content is hashed, not retained by the detector. Six consecutive repeated observations without new evidence produce one visible notice and a model-facing request to use the collected evidence or identify a specific blocker. A new observation, changed result, or potentially mutating/unknown tool resets the episode. State is bounded and per-loop; main chat does not enable the notice.

This is deliberately a warning/steering intervention, not an automatic kill switch. Read-only research is legitimate: lack of edits by itself never triggers it. The detector does not establish absence of semantic progress, catch all cycles, or promise to stop every reconnaissance loop. Broader abort thresholds and task-specific execution strategy remain investigation topics.

## Unchanged policy

Compaction remains the first-class Secondary/Economy task by default. Main/dispatch routing, backup policy and six-minute execution ceiling are unchanged. Activation thresholds, model temperature and reasoning settings are not tuned here. No global token-cap increase, provider restart, automatic retry, or destructive migration was introduced.

## Verification

Failing pre-fix probes covered ignored Read fields, destructive missing Write content, split tool exchanges, unsafe freeze boundaries, unrelated-runtime budget lookup, ignored reported summarizer usage, and missing exhaustion handoffs. Deterministic coverage includes parallel tool calls, local chunk deferral, route/window changes and isolation, reported/estimated/known-zero accounting, no-call failures, cache inclusivity, budget stops before another request, native and flattened-history progress notices, error classification, schema concurrency, and existing package regressions.

## Investigations after landing/deployment

1. Verify current GLM HTTP-level reasoning settings and continuation, including after compaction, using bounded authenticated captures. Stored sanitized history is not final wire evidence.
2. Replay recorded spans to evaluate summary quality beyond structural pairing: retained findings, completed versus pending state, and redundant rereads.
3. Measure compaction frequency and segment cadence separately from summarizer routing latency. Do not attribute the configured 40k floor to the retained-summary window calculation.
4. Compare generic versus implementation-specific instructions on small, identical tasks with testable deliverables.
5. Only then compare model-appropriate generation settings, with all other inputs held fixed. Measure useful verified work and repeated observations, not merely a final answer.

No further broad, unbounded implementation dispatch is needed to perform these checks.
