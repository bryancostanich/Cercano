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

1. **Read-only task reference — dispatches only.** `compaction.WithTaskReference`
   supplies an *assigned* task to the summarizer as reference data for selecting
   relevant facts. The protected task itself is still never compaction input and
   is not rewritten. The reference is JSON-encoded, explicitly marked as data,
   and explicitly not evidence of approval, execution, or completion. It counts
   toward the summary request budget and defers rather than silently overflowing.

   A main conversation has **no assigned task**, and one is never inferred.
   Its trailing user message is typically conversational — real examples from
   this database include `"push"`, `"continue"`, `"land on main"`, and
   `"middle button drag left/right is backwards"`. Presenting any of those as
   the worker's objective would misdirect fact selection and could discard a
   whole session's findings as irrelevant. Background main-chat compaction
   instead passes `WithUserIntentHint`, which reaches the rejection gate's
   narrow exemptions and is never placed in the prompt.
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

## Both compaction paths

The change covers the two places compaction runs, because the failure was
observed in a sub-agent dispatch but the same summarizer serves main turns:

- **Sub-agent / in-loop** (`loopcompact.Compactor`, per dispatch, synchronous):
  `agent.RunToolLoop` stamps `spec.Task` via `WithTaskReference`, and each
  `Compactor` is built with its own `SummaryGuard`. One dispatch exhausting its
  two-attempt budget cannot suppress compaction for another dispatch. This is
  the only path that sets `LoopCompactor`, so the stamp is unambiguously an
  assigned task.
- **Main thread** (`compactiongen.Generator`, store-backed, background):
  `runCompaction` and `Regenerate` derive an *intent hint* from the latest real
  user turn — gate-only, never prompted as an objective. Background guards are
  per conversation *and* intent digest; explicit regeneration always gets a
  fresh budget.

These are the two compaction paths by design: main chat compacts in the
background, dispatches compact in-loop. `runner/core.go` therefore sets no
`LoopCompactor`, and a test asserts that `internal/hostsvc/tools/tools.go`
remains its only non-test caller — if that ever changes, the in-loop task stamp
would need revisiting, so the test fails rather than letting it pass silently.

Rejection degrades safely rather than failing a turn: the compactor returns the
quality error, and `agent.compactLoopHistory` converts that into unchanged
history so the loop continues uncompacted.

## Verification

`internal/compaction/recorded_failures_test.go` replays the ten real recorded
outputs (model text only, no transcripts, reasoning, or credentials): eight
code-span summaries are now rejected, nine meta-objectives are detected, and a
substantive replacement summary for the same span is accepted. Additional tests
cover the reference contract, budget deferral, findings parse/merge/render,
same-file distinct observations, bounded rejection, task-change reset, separate
regeneration budget, and that rejection leaves raw turns and stored compaction
state untouched.

`internal/compaction/intent_scope_test.go` pins the task/intent split using real
trailing main-thread messages: none of them may become an assigned task or reach
the prompt, an assigned dispatch task still must, and the gate's exemptions still
see the user's wording.

`internal/loopcompact/guard_scope_test.go` covers the sub-agent path
specifically: the per-dispatch guard bound (two summarizer calls, then no more),
budget independence across dispatches from the same factory, and that the
in-loop summarizer receives the task reference and reduces history when the
summary is substantive.

Verified with `go test ./internal/compaction ./internal/compactiongen
./internal/loopcompact ./internal/agent ./internal/hostsvc/tools
./internal/compactor`, then a full `go test ./...`: 103 packages pass, no
failures. Changed files are gofmt-clean; unrelated pre-existing gofmt dirt
elsewhere in the repository is untouched.

No live model comparison was run for this change.
