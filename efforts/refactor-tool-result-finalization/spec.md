# Spec: Refactor tool-result finalization seam

## Problem

The tool-result payload cap effort (`efforts/cap-tool-result-payloads`) found that `agent.RunToolLoop` has two independent tool-execution/result-finalization paths:

- the parallel read-tier path, used by R-tier tools such as `Grep`, and
- the sequential path, used for tools that cannot run in the parallel read batch.

The production incident took the parallel R-tier path. The initial window guard was applied only to the sequential path, and the end-to-end reconstruction still failed with:

```text
preflight context_overflow (94733 tokens used vs 32768 limit)
```

That matched the live failure (`94632 vs 32768`) and proved that two call sites can drift. The immediate fix applied the same `capToolResultForWindow` helper at both sites, so the current behaviour is correct, but the structure still permits future drift if result-finalization logic changes or a third execution path is added.

## Goal

Create one shared finalization seam for successful tool results before they enter conversation history. All tool-execution paths should route through it.

The shared seam should own:

- `res.LLMContent()` rendering,
- context-window result capping,
- `BlockToolResult` construction via `toolResultBlocks`,
- source line metadata propagation,
- truncation/capping logging,
- and any future model-facing result annotations.

## Non-goals

- Do not change the truncation/capping policy itself.
- Do not change parallel execution semantics or ordering.
- Do not change permission handling or tool execution.
- Do not merge `agenttools.Result` and `capabilities.Result` as part of this effort.

## Acceptance criteria

- Parallel and sequential tool-execution paths both call one shared finalization helper.
- Existing result-capping tests continue to pass, including the oversized `Grep` reconstruction.
- Tests cover both parallel R-tier and sequential W/X-tier result finalization.
- `go test ./internal/agent` passes.
- Broader `go test ./...` passes if shared loop code changes substantially.

## Priority

Medium. This is structural hardening, not a known live hole after `291546f162e2`; the current cap helper is applied at both known paths. It is still worth doing because the missed parallel path was found by regression testing, not by inspection, so consolidating the seam reduces future risk.
