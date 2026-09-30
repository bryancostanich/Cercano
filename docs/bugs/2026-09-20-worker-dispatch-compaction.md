# Worker dispatch compaction parity and observability

## Confirmed defects

Worker tool-service construction did not install the per-dispatch inline compactor installed by the host. The asynchronous persisted-history generator cannot reduce a running dispatch's in-memory request history. Removing the new worker installation makes `TestBuildWorkerToolSvc_WiresLoopCompactor` fail with the missing-factory diagnostic.

The worker configuration snapshot also omitted `SummarizerModel`, `CompactedBudgetPct`, and `TieredRetentionSegments`. Those settings now round-trip through additive protobuf fields.

Evidence-preservation probes uncovered two pre-existing inline-compactor defects:

- Synthetic timestamp zero placed the first message at the initial frozen boundary, excluding it from the eligible history.
- Reusing array-index timestamps after the caller fed back a reduced view placed unsummarized live messages behind the previous frozen boundary. A second threshold-crossing pass could silently omit those messages. Reduced views now anchor their summary at the frozen boundary and their live tail strictly after it.

`TestRepeatedPassDoesNotSkipUnsummarizedMessages` failed first on message 0, then on message 84 after the zero-origin fix alone. It now verifies that every original and newly accumulated text message is either passed to the summarizer or retained in the outgoing view. This checks evidence delivery, not a real model's summary fidelity.

## Implementation

Host and worker share summarizer/config construction in `internal/loopcompact/wiring.go`. Each dispatch owns a fresh compactor. Worker summarization uses worker providers, host runtime-capacity access when available, the explicit summarizer override or fast-light-text lane, and existing cloud fallback rules. No database is opened in the worker. Budgets and activation defaults are unchanged.

The inline policy activates at an absolute estimated-history threshold (default 40,000 tokens), not a percentage of the delegated model's window. Default segment size is 8,000 tokens with six recent turns retained; the algorithm caps each pass at four segments. The compacted-backlog budget is derived from the everyday open model's window, with the existing 30% default and 16,000-token floor. This policy still warrants workload-specific validation; installing it is not evidence of optimal cost or summary fidelity.

## Telemetry

- `[loop-compaction] dispatch`: conversation ID and whether a compactor is configured.
- `[loop-compaction] pass`: conversation/iteration, effective thresholds, outcome and classified reason, message/token estimates before and after, tool-result counts and content sizes before and after, summarizer invocation count, estimated spend, and duration.
- Summarizer request IDs include dispatch correlation; usage lines distinguish provider-reported usage from estimates, including cloud fallback.

New diagnostics contain metadata only, not prompt/tool-result text or raw provider errors. This does not remove pre-existing content-bearing logs elsewhere in the application. Failure remains advisory and preserves history. The existing compactor spend estimate is not a complete accounting of chunk/merge/output usage; reported usage is now visible for comparison rather than silently presented as equivalent.

## Verification and limits

Focused tests cover worker construction, actual outgoing model requests, recent evidence and tool pairing, independent dispatch state, disabled/no-provider paths, cloud fallback default model, snapshot round trips, repeated-pass evidence preservation, and telemetry outcomes. Worker/compactor race tests also pass.

No historical workload replay or real-model completion experiment has been run. No live binary has been installed or restarted. These remain deployment validation, not proven outcomes of the deterministic tests.
