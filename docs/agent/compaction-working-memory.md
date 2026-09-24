# Compaction working memory

Compaction previously replaced investigation history with receipts that a file
had been read, and sometimes asserted that work was finished or awaiting
instructions while an implementation task was still active. A worker then reread
source it had already inspected. This change targets that failure.

## Evidence

In dispatch `a4bfafd8578002fc6b9ab73c`, 13 summarizer calls produced ten
nonempty outputs. Nine described summarization itself as the goal. At iteration
18 the `SiteMeshJobKey` and `SiteMeshJobRunner` declarations were present before
compaction and absent afterward, replaced by a path plus content hash; the next
tool call reread the same range. One 23,572-character span became 76 characters
of "summarize / awaiting further instruction" while reporting 497 output tokens
against a 1,024-token allowance, so truncation alone does not explain it.

## Changes

1. **Read-only task reference.** `compaction.WithTaskReference` supplies the
   active task to the summarizer as reference data for selecting relevant facts.
   The protected task itself is still never compaction input and is not rewritten.
   The reference is JSON-encoded, explicitly marked as data, and explicitly not
   evidence of approval, execution, or completion. It counts toward the summary
   request budget and defers rather than silently overflowing.
2. **A FINDINGS section.** `StructuredSummary.Findings` carries historical
   observations: behavior, constraints, call paths, and verified results with
   exact identifiers. `FILES` returns to actual modification/build state. The
   existing fields, JSON shape, and parser stay compatible; older stored
   summaries keep working, and findings-only summaries are not empty.
3. **A rejection gate.** `ValidateWorkingMemory` rejects a summarization-of-the-
   transcript objective, invented idle/finished claims, and receipt-only content
   for spans that actually contained substantive inspected code or search output.
   Rejected summaries are not persisted, so raw history is preserved instead.
4. **Bounded retries.** `SummaryGuard` allows at most two rejected attempts per
   dispatch, or per conversation task for the background generator. Exhaustion
   stops further calls rather than looping. A new task or an explicit
   regeneration gets its own budget; background exhaustion does not block
   explicit regeneration. Background guard state is bounded to 128 conversations
   and keyed by task digest, not task text.

## Limits

This is prompt guidance plus a conservative mechanical gate. It does not
semantically verify summaries, prove factual accuracy, or repair summaries saved
earlier. The gate is intentionally scoped to spans with substantive inspected
evidence, so a poor summary of a prose-only span still passes.

It does not by itself prove GLM-5.3 will complete the aperture-reuse task: the
isolated baseline run made zero edits without ever hitting a response cutoff,
and rereading was only one observed contributor. Three of 24 summaries in that
run were also truncated at the 1,024-token summarizer cap; that cap is unchanged
here.

Rejection trades tokens for fidelity: a rejected pass still bills its model call
and leaves the history uncompacted, which can bring the loop closer to its
context limit. The two-attempt bound and preserved raw history are the mitigation.

## Verification

`internal/compaction/recorded_failures_test.go` replays the ten real recorded
outputs (model text only, no transcripts, reasoning, or credentials): eight
code-span summaries are now rejected, nine meta-objectives are detected, and a
substantive replacement summary for the same span is accepted. Additional tests
cover the reference contract, budget deferral, findings parse/merge/render,
same-file distinct observations, bounded rejection, task-change reset, separate
regeneration budget, and that rejection leaves raw turns and stored compaction
state untouched.

Verified with `go test ./internal/compaction ./internal/compactiongen
./internal/loopcompact ./internal/agent ./internal/hostsvc/tools
./internal/compactor` and a full `go test ./...` run. Two unrelated timing
tests (`TestDownloadModel_ConcurrentSameModelDownloadsOnce`,
`TestWorkerAccountingDrainReportsUnstoppedProducer`) failed in the full run and
passed repeatedly in isolation.

No live model comparison was run for this change.
