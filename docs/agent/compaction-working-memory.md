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
   instead passes `WithUserIntentHint`, which is never placed in the prompt.
2. **A FINDINGS section.** `StructuredSummary.Findings` carries historical
   observations: behavior, constraints, call paths, and verified results with
   exact identifiers. `FILES` returns to actual modification/build state. The
   existing fields, JSON shape, and parser stay compatible; older stored
   summaries keep working, and findings-only summaries are not empty.
A rejection gate and retry budget were built here and then **removed**. See
"Why the rejection gate was removed" below before reintroducing anything similar.

## Why the rejection gate was removed

The gate refused summaries that reported summarization as the goal, invented
idle/finished claims, or reduced inspected code to file/hash receipts. Audited
against the ten recorded summaries it judged correctly: 8 rejected, 2 accepted.

It was still removed, because correct judgment was not the same as useful
behavior:

- **It punished the summarizer for a faithful report.** Thin summaries can mean
  the transcript itself was thin. In the reproduction the worker re-read one
  file seven times, so there was genuinely little to summarize. The gate reads
  that as summarizer failure and refuses the summary.
- **Refusal was worse than the summary.** A refused pass leaves history
  uncompacted. In a growing agent loop that is not a safe resting state: a live
  run showed compaction stalling with history pinned at 38 messages for ten
  consecutive iterations, each attempt failing in ~10 ms without a model call.
- **It fired hardest exactly when it hurt most.** The transcripts most likely to
  produce thin summaries are the ones already going badly.

The underlying failure was never compaction. The Read tool truncates at 32 KiB
and appends "refine to get more", which invites the model to re-read; one 32,826
byte file overflowed by 58 bytes and was re-read seven times. Compaction
accounted for 5 of 70 tool calls in that run.

What remains is the part that helps without refusing anything: the task
reference, the FINDINGS section, and the prompt guidance.

## Limits

This is prompt guidance and schema only. Nothing here verifies summaries,
rejects a poor one, or repairs summaries saved earlier: the summarizer is asked
for better output and its answer is used as given.

It does not by itself prove GLM-5.3 will complete the aperture-reuse task: the
isolated baseline run made zero edits without ever hitting a response cutoff,
and rereading was only one observed contributor. Three of 24 summaries in that
run were also truncated at the 1,024-token summarizer cap; that cap is unchanged
here.

## Both compaction paths

The change covers the two places compaction runs, because the failure was
observed in a sub-agent dispatch but the same summarizer serves main turns:

- **Sub-agent / in-loop** (`loopcompact.Compactor`, per dispatch, synchronous):
  `agent.RunToolLoop` stamps `spec.Task` via `WithTaskReference`, and each
  `Compactor` is built with its own `SummaryGuard`. This is the only path that sets
  `LoopCompactor`, so the stamp is unambiguously an assigned task.
- **Main thread** (`compactiongen.Generator`, store-backed, background):
  `runCompaction` and `Regenerate` derive an *intent hint* from the latest real
  user turn, which is never prompted as an objective.

These are the two compaction paths by design: main chat compacts in the
background, dispatches compact in-loop. `runner/core.go` therefore sets no
`LoopCompactor`, and a test asserts that `internal/hostsvc/tools/tools.go`
remains its only non-test caller — if that ever changes, the in-loop task stamp
would need revisiting, so the test fails rather than letting it pass silently.

Rejection degrades safely rather than failing a turn: the compactor returns the
quality error, and `agent.compactLoopHistory` converts that into unchanged
## Verification

`internal/compaction/intent_scope_test.go` pins the task/intent split using real
trailing main-thread messages: none of them may become an assigned task or reach
the prompt, and an assigned dispatch task still must.

`internal/loopcompact/guard_scope_test.go` pins the wiring invariant: only the
dispatch path may set a LoopCompactor, and the in-loop summarizer receives the
assigned task reference.

Verified with `go test ./internal/compaction ./internal/compactiongen
./internal/loopcompact ./internal/agent ./internal/hostsvc/tools
./internal/compactor`, then a full `go test ./...`: 103 packages pass, no
failures. Changed files are gofmt-clean; unrelated pre-existing gofmt dirt
elsewhere in the repository is untouched.

No live model comparison was run for this change.
