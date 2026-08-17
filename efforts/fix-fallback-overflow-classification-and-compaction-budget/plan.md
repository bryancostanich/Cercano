# Plan: Fallback overflow classification and local compaction budgeting

Effort: `efforts/fix-fallback-overflow-classification-and-compaction-budget`
Spec: `efforts/fix-fallback-overflow-classification-and-compaction-budget/spec.md`

## Phase 1 — Fix OpenAI Responses context-overflow classification

- [x] Update `source/server/internal/llm/responses/stream.go` so stream errors run context-window text through `llm.DetectContextOverflow(...)` before the generic `invalid_request` classification.
- [ ] Add or update tests for the OpenAI Responses stream adapter covering the exact text:
  - [x] `Your input exceeds the context window of this model. Please adjust your input and try again.`
  - [x] a generic invalid request that should remain `llm.ErrInvalidRequest`.
- [x] Confirm non-streaming Responses error normalization already detects context overflow; if not, apply the same ordering there.

## Phase 2 — Make tight local fallback activation defensive

- [x] In `source/server/internal/runner/core.go`, factor a helper such as `isContextOverflowForFallback(err error) bool`.
- [x] Return true when `llm.ClassOf(err) == llm.ErrContextOverflow`.
- [x] Also return true when `llm.DetectContextOverflow(err.Error())` matches context-window wording.
- [x] Use that helper when setting `tightContextFallback` for cloud-to-local retry.
- [x] Add or update a runner test where the primary/cloud provider returns an error classified as `ErrInvalidRequest` but whose text says context window exceeded; assert the local retry uses compact fallback catalog.
- [x] Keep existing behavior for non-context invalid requests: they may still retry according to route policy, but they must not force compact fallback unless the text indicates context overflow.

## Phase 3 — Introduce compaction request budgeting

- [ ] Add a small budget helper for compaction summarizer calls, preferably in `source/server/internal/compaction` or a nearby package, that estimates rendered prompt tokens plus output reserve against a context window.
- [ ] Reuse the existing approximate token estimator where practical; avoid duplicating constants without tests.
- [ ] Define a local compaction output reserve, initially around 1024–2048 tokens, and make it configurable only if there is an appropriate existing compaction config surface.
- [ ] Add structured budget results for logs/tests: prompt estimate, output reserve, limit, effective budget, fit/overflow.
- [ ] Unit-test that a prompt just under budget fits and a prompt that only exceeds because of output reserve fails.

## Phase 4 — Add local adaptive chunking for summarizer input

- [ ] Add a function that packs `[]llm.Message` into message-boundary-preserving chunks where `compaction.BuildSummaryPrompt(chunk)` plus local output reserve fits the configured local context window.
- [ ] Keep chronological order inside chunks.
- [ ] If a single message cannot fit, return a typed deferral/error that identifies the minimal-unit overflow instead of truncating silently.
- [ ] Add tests for:
  - [ ] one fitting chunk,
  - [ ] multiple chunks,
  - [ ] one oversized single message causing deferral/error,
  - [ ] stable ordering.

## Phase 5 — Summarize chunks and merge locally

- [ ] Refactor the compaction summarizer closure in `source/server/cmd/cercano/main.go` so the local path can call a helper that performs budgeted local summarization.
- [ ] For fitting inputs, preserve the existing single-call behavior except for the smaller compaction output reserve.
- [ ] For oversized inputs, summarize each chunk locally.
- [ ] Merge multiple parsed summaries locally by rendering the chunk summaries as compact input and requesting one final structured summary.
- [ ] If the merge prompt is too large, merge recursively in budgeted batches.
- [ ] Preserve the existing `compaction.ParseSummary` behavior and empty-summary guard.
- [ ] Tests should use fake providers; do not require real local or cloud models.

## Phase 6 — Make cloud fallback optional and non-disruptive

- [ ] Keep cloud fallback for local runtime failures when cloud is available.
- [ ] Do not use cloud fallback as the only solution to an oversized local prompt; chunk locally first.
- [ ] If local chunking cannot fit a minimal unit and cloud is unavailable or also fails, return/handle a non-fatal compaction deferral.
- [ ] Ensure compaction deferral is logged as compaction work, not as a sub-agent/tool-loop failure.
- [ ] Add tests or a focused integration-style fake that verifies oversized local compaction succeeds without cloud via chunking.
- [ ] Add a test or assertion that minimal-unit overflow is handled as deferral rather than surfacing through an unrelated active agent run.

## Phase 7 — Diagnostics

- [ ] Add compaction-specific diagnostic logs around local summarizer requests:
  - [ ] conversation id when available,
  - [ ] compaction request/pass id,
  - [ ] route: local/cloud,
  - [ ] prompt estimate,
  - [ ] output reserve,
  - [ ] context limit,
  - [ ] chunk count,
  - [ ] merge pass count.
- [ ] If the existing provider diagnostics can carry request IDs for non-tool `Chat` calls, populate them for compaction requests.
- [ ] Verify future logs can distinguish a compaction overflow from a sub-agent overflow.

## Phase 8 — Verification and checkpoint

- [ ] Run focused tests:
  - [ ] `go test ./internal/llm/responses`
  - [ ] `go test ./internal/runner`
  - [ ] `go test ./internal/compaction`
  - [ ] any new helper package tests.
- [ ] Run broader server verification:
  - [ ] `go test ./...`
- [ ] Review logs/test assertions for the two original failure shapes:
  - [ ] cloud context-window stream error activates compact local fallback,
  - [ ] oversized local compaction input is chunked/deferred locally, not sent as a doomed single request.
- [ ] Checkpoint the completed work with a clear commit subject and body.

## Notes

- The classification/fallback fix is smaller and should land first; it unblocks the already-implemented compact catalog behavior.
- The compaction fix is the correctness path for offline/local operation. Cloud fallback remains useful, but cloud availability must not be required for compaction to make progress.
- Avoid silent truncation. If data must be omitted, it should happen through explicit compaction/chunking semantics or a typed deferral.
