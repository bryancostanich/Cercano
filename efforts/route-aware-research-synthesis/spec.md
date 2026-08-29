# Route-aware local research synthesis

## Problem

The `research` and `deep_research` tools build large one-shot synthesis prompts and send them to the local co-processor model without first sizing those prompts against the concrete local model/runtime that will receive them.

The recent route-aware context fallback work fixed this class of problem for main chat history assembly, but research has a different input shape: fetched source excerpts and annotated findings rather than persisted conversation turns. Research has always used a local model, so the regression is not cloud-vs-local routing. The regression is that local research synthesis can now target a concrete llama-server model/window, such as a 16,384-token GLM runtime, while still constructing prompts from fixed character caps and hoping they fit.

Observed failure shape:

```text
research failed: synthesis failed: openai context_overflow (500)
```

The `openai` label is from the OpenAI-compatible llama-server adapter, not cloud OpenAI. The underlying failure is local context overflow during research synthesis.

## Goals

- Keep research local-model-first.
- Give research synthesis access to the concrete dispatch target: provider, model, tier, cloud/local flag, and known context window when available.
- Build research synthesis prompts to a safe input budget after reserving output tokens.
- Replace magic fixed source-size caps with deterministic budget-aware packing/trimming.
- Preserve source metadata and citation identity when trimming content.
- Apply the same discipline to `deep_research` synthesis calls over annotated findings.
- Add regression tests with tiny fake windows proving large research inputs are budgeted before the model call.
- Surface clear errors when a prompt cannot be made safe because the target window is unknown or too small.

## Non-goals

- Do not root-cause or tune live llama-server HTTP 500 `Compute error.` failures in this effort.
- Do not route research synthesis to cloud as a fallback.
- Do not force research through the conversation-history `requestassembly` package; research source material is not conversation history.
- Do not change search/fetch provider behavior beyond what is necessary to budget synthesis prompts.
- Do not alter the main chat route-aware context fallback behavior except for shared helper reuse if needed.

## Design direction

Introduce a small budget-aware model-call seam for research. The dispatch-backed model caller should be able to report the concrete target and a usable input budget before the research package constructs large prompts.

Research prompt builders should use that budget to pack source material or finding summaries deterministically. The model call should still be local and one-shot where the budget fits. For larger deep-research reports, synthesis substeps should each budget their own finding summaries rather than concatenating unbounded text.

If the concrete target has a known context window, derive:

```text
safe_input_budget = context_window - output_reserve - protocol_overhead_margin
```

If the target window is unknown, use a conservative default or return a clear budget error, depending on the calling path. The implementation should prefer loud failure over silently submitting an over-sized local request.

## Acceptance criteria

- `research` synthesis no longer sends a prompt larger than the calculated local target input budget.
- `deep_research` synthesis helpers no longer concatenate unbounded finding summaries without checking a local target budget.
- Tests cover budgeted source packing/trimming under a deliberately tiny context window.
- Tests cover target metadata propagation from dispatch-backed model caller to research code.
- Existing research behavior remains compatible for normal local models with sufficient context.
- Focused tests for changed packages pass.
