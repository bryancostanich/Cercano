# Spec: Fallback overflow classification and local compaction budgeting

## Problem

Two related overflow failures remain after the compact tool catalog work.

### 1. Cloud context overflow is sometimes classified as `invalid_request`

In `LUNIE - LUNIE VIEW INTEGRATION III`, the cloud OpenAI Responses stream failed with:

```text
openai-responses invalid_request: stream error: Your input exceeds the context window of this model. Please adjust your input and try again.
```

The runner retried on `llama_server`, but the local retry did not activate tight-context fallback / compact tools. The local preflight then failed with:

```text
preflight context_overflow (22322 tokens used vs 16384 limit): request is ~22322 tokens including ~12522 tool tokens and 8192 reserved output tokens
```

The compact catalog code can exist and still not run because the runner currently marks a local retry as tight-context fallback only when `llm.ClassOf(loopErr) == llm.ErrContextOverflow`. The OpenAI Responses streaming adapter classified the context-window message as `ErrInvalidRequest` before checking the message text for context overflow.

### 2. Background local compaction/summarization can overflow local context

A separate sub-agent-looking failure was traced to background compaction/summarizer calls, not to the sub-agent prompt itself. The sub-agent request was small, but local compaction sent a non-tool request shaped roughly like:

```text
messages=1
message_bytes=56962
tools=0
approx_prompt_tokens=14241
max_tokens=4096
local context=16384
```

That can exceed the local model's usable window once output reserve is included. The compaction path builds one large `compaction.BuildSummaryPrompt(msgs)` and calls `openProvider.Process`; it does not use the tool-loop request budget helper and does not locally split/merge when the prompt is too large.

Cloud fallback cannot be the correctness strategy here. If cloud is unavailable, expired, failing, or also over context, compaction still needs a local-only path that can make progress under the configured local context window. If even a minimal local compaction unit cannot fit, compaction should defer cleanly rather than poisoning active turns or making a small sub-agent look like it failed.

## Goals

1. Classify OpenAI Responses stream context-window failures as `llm.ErrContextOverflow`.
2. Make runner tight-context fallback activation defensive so one missed provider classification cannot bypass compact local tools again.
3. Budget local compaction/summarizer requests against the selected local context window before calling the provider.
4. Reduce local compaction output reserve to a realistic structured-summary reserve instead of using an oversized generic chat reserve.
5. Add local adaptive chunking: if the full compaction prompt cannot fit, split the input messages into budgeted chunks, summarize chunks locally, then merge chunk summaries locally under budget.
6. If even the smallest local chunk cannot fit, defer compaction with clear logs instead of surfacing the error as a tool-loop/sub-agent failure.
7. Preserve cloud fallback as optional recovery/acceleration, not as required correctness behavior.
8. Improve diagnostics so compaction requests have identifiable logs and cannot be confused with sub-agent requests.

## Non-goals

- Do not increase local model context size as the primary fix.
- Do not depend on cloud fallback for compaction correctness.
- Do not disable compaction globally.
- Do not replace the existing compaction parser/summary format.
- Do not rework the entire compactor retention policy unless tests show the summarizer API needs a small seam.

## Design direction

### A. Provider classification and runner fallback

`source/server/internal/llm/responses/stream.go` should check stream error messages with `llm.DetectContextOverflow(...)` before falling through to generic `invalid_request` classification. The exact phrase from OpenAI Responses should be covered:

```text
Your input exceeds the context window of this model. Please adjust your input and try again.
```

`source/server/internal/runner/core.go` should also make tight local fallback defensive. When falling from cloud to a non-cloud provider, set `TightContextFallback` if either:

- `llm.ClassOf(loopErr) == llm.ErrContextOverflow`, or
- `llm.DetectContextOverflow(loopErr.Error())` returns true.

This keeps compact tool catalog activation correct even if a future provider adapter misses classification.

### B. Local compaction budgeting

Add a small compaction budgeting layer for local summarizer calls. It should estimate:

- rendered summary prompt tokens,
- requested output reserve,
- selected local context limit,
- safety margin.

The existing four-characters-per-token style estimate is acceptable if conservative and tested. Provider usage remains authoritative.

The local summarizer should reserve fewer output tokens than the generic chat loop. Structured compaction summaries should start with a local reserve around 1024–2048 tokens, configurable if there is already a compaction config surface. The budgeter must include this reserve.

### C. Adaptive local chunking and merge

When `BuildSummaryPrompt(msgs)` does not fit locally:

1. Pack messages into chunks whose rendered prompt plus output reserve fit the local context window.
2. Summarize each chunk locally using the same structured summary format.
3. If there is more than one chunk, merge the chunk summaries by rendering them as compact input and asking the local model to produce one structured summary under the same budget.
4. If the merge prompt is too large, merge recursively in batches.
5. Return the final parsed `compaction.StructuredSummary`.

Chunking should happen in the compaction/summarizer layer or a small helper close to it, not by truncating raw text in `cmd/cercano/main.go`. The compactor already reasons about message boundaries; the summarizer should preserve message boundaries when packing chunks.

### D. Defer instead of disrupt

If a single message plus summary instructions cannot fit even with the reduced output reserve, the compaction pass should return a typed/declarative defer error or otherwise be handled as a non-fatal compaction deferral. Logs should say compaction was deferred because the minimal local summary unit cannot fit the configured local context.

This error must not be attributed to an unrelated active tool loop or sub-agent.

### E. Diagnostics

Local compaction calls should include diagnostic metadata equivalent to conversation/request IDs where possible. At minimum logs should include:

- compaction pass/request id,
- parent conversation id if available,
- local/cloud route,
- prompt estimate,
- output reserve,
- context limit,
- chunk count and merge pass count.

## Acceptance criteria

- An OpenAI Responses stream error containing `Your input exceeds the context window` is classified as `llm.ErrContextOverflow`.
- Runner fallback to local sets `TightContextFallback` for context-window text even if the error class is not already `ErrContextOverflow`.
- A regression test proves the local retry after cloud context overflow uses the compact catalog path.
- Local compaction preflights prompt plus output reserve before calling the local provider.
- A long compaction input that would exceed a 16k local window is chunked into fitting local summarizer calls rather than sent as one oversized request.
- Multi-chunk summaries are merged locally under budget.
- If a minimal chunk cannot fit, compaction defers cleanly and does not fail an active sub-agent/tool loop.
- Cloud fallback still works when local runtime errors occur and cloud is available, but tests do not require cloud for oversized local compaction success.
- Focused tests and relevant broader server tests pass.
