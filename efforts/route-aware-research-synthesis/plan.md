# Plan: route-aware local research synthesis

## Phase 1 — Establish the model target/budget seam

- [x] Inspect all current `research.ModelCaller` implementations and tests.
- [x] Add a small budget-aware interface for research callers, for example:
  - `Target() ModelTarget` or `Budget(ctx) ModelBudget`
      - fields: provider, model, tier, is_cloud, context_window, context_window_known
      - safe input budget after output reserve/margin
- [x] Implement the interface in the dispatch-backed caller used by built-in `research` and `deep_research`.
- [x] Resolve the concrete model/window using the same local dispatch selection already used for the actual model call.
- [x] Add unit tests for target/budget reporting with a fake dispatch result or fake caller.

## Phase 2 — Budget simple `research` synthesis prompts

- [x] Replace fixed per-result source caps in `source/server/internal/web/research.go` synthesis with token/window-aware packing.
- [x] Preserve each included source's title and URL even when content is trimmed.
- [x] Reserve enough output tokens for the requested synthesis response.
- [x] Make oversized-source trimming deterministic and testable.
- [x] Return a clear error if no useful source content can fit.
- [x] Add regression tests proving a tiny fake window causes trimming before `ModelCaller.Call` is invoked.

## Phase 3 — Budget `deep_research` synthesis helpers

- [x] Update `source/server/internal/research/synthesis.go` helper prompts to budget `buildFindingSummaries(...)` output against the caller budget.
- [x] Ensure these functions do not ignore budget information when the model caller provides it.
- [x] Preserve finding titles/source identifiers in summaries where possible.
- [x] Keep existing behavior for callers that only implement legacy `Call(ctx, prompt)`.
- [x] Add tests for at least executive summary or narrative synthesis with many large findings and a tiny fake window.

## Phase 4 — Improve diagnostics

- [x] Add prompt-budget metadata to errors or progress where useful: model, provider, context window, input budget, estimated prompt tokens, and trimmed source/finding counts.
- [x] Ensure `context_overflow` from the provider still propagates if the provider rejects a budgeted request.
- [x] Avoid logging source bodies or prompt text.

## Phase 5 — Verification and cleanup

- [x] Run focused tests for changed packages, expected set:
  - `go test ./internal/web ./internal/research ./internal/capabilities/builtins ./internal/dispatch`
- [x] Run broader changed-package tests if imports require it.
- [-] Manually exercise a small local `research(max_results=5)` scenario if feasible without relying on external flakiness.
- [x] Update this plan with final status and any deferred follow-ups.
- [x] Commit a checkpoint with a conventional commit subject and body.

## Final status

Implemented. Research and deep-research synthesis now use an optional budget-aware local model interface. The dispatch-backed built-in caller resolves the same concrete target used for execution, including config/catalog-aware local context windows, and prompt builders deterministically trim source/finding material before calling the model. The manual live research exercise was skipped because it would depend on external search/network and local-runtime availability; focused and broad internal Go tests cover the budget behavior.

## Risks and mitigations

- Risk: token estimation differs from llama-server's tokenizer, so a prompt can still overflow near the boundary.
  - Mitigation: keep a conservative protocol overhead/output reserve/margin and test using intentionally tiny budgets.
- Risk: trimming source excerpts harms research quality.
  - Mitigation: preserve titles/URLs and trim content deterministically rather than dropping identity metadata.
- Risk: interface changes ripple into unrelated model callers.
  - Mitigation: make the budget interface optional; legacy `ModelCaller` remains valid.
- Risk: deep-research synthesis currently ignores individual helper errors in some places.
  - Mitigation: keep this effort focused on budget-safe prompt construction, but surface budget errors where helpers already return errors.
