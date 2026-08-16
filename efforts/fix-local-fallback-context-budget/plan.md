# Plan: Local fallback context budgeting and compact tool catalogs

Effort: `efforts/fix-local-fallback-context-budget`
Spec: `efforts/fix-local-fallback-context-budget/spec.md`

## Phase 1 — Add a reusable request budget helper

- [x] Introduce a focused budget helper near the agent/tool-loop code, using `llm.Message` and `llm.Tool` as inputs.
- [x] Move or wrap the existing estimates from `internal/agent/preflight.go` so the helper can calculate:
  - [x] system prompt cost,
  - [x] message/history cost,
  - [x] image placeholder/image estimate cost,
  - [x] serialized tool schema cost,
  - [x] output-token reserve from `MaxTokens`,
  - [x] effective prompt budget from `ContextWindow` and a safety fraction.
- [x] Return a structured result with enough fields for logs and tests: estimated used tokens, fixed tokens, history tokens, tool tokens, output reserve, limit, budget, fit/overflow, and trimmed message count.
- [x] Unit-test the helper with:
  - [x] messages only,
  - [x] tools only,
  - [x] messages plus tools exceeding the window,
  - [x] output reserve pushing an otherwise-fitting prompt over budget.

## Phase 2 — Enforce the budget on the exact tool-loop request

- [x] In `agent.RunToolLoop`, build/filter the tool catalog before context reduction.
- [x] Apply image placeholder rewriting before final budgeting, so the estimate matches the provider-facing message list.
- [x] Replace `reduceHistoryToContextTail` / `preflightContextCheck` with the new helper or adapt them to call it.
- [x] Trim oldest prior-history messages until the request fits the selected provider window, preserving the current user message and system prompt.
- [x] Repair tool-use/tool-result pairing after trimming with the existing pairing repair function.
- [x] Run the budget check before every provider request, not only before iteration 1, because assistant/tool-result messages can grow the prompt between iterations.
- [x] If the minimal request still does not fit, return `llm.ErrContextOverflow` with estimated used/limit values and do not call the provider.
- [x] Emit/log a concise notice when trimming happens, including previous history count, new history count, estimated prompt tokens, tool tokens, output reserve, and local limit.
- [x] Tests:
  - [x] oversized history is trimmed before provider call,
  - [x] tool schemas are counted,
  - [x] no provider call occurs when the request cannot fit,
  - [x] valid pairing after trimming.

## Phase 3 — Mark tight local fallback explicitly

- [x] In `runner.Core.runLoop`, when cloud/openai-responses fails with `context_overflow` and retry selects a smaller local provider, set an explicit flag on `agent.ToolLoopInput` such as `TightContextFallback` or `CompactToolCatalog`.
- [x] Avoid deriving behavior from provider name alone. The flag should describe the state: this is a retry into a smaller context after an overflow or a similarly constrained path.
- [x] Keep the existing user-facing notice `local context is smaller — using recent conversation tail only`, but update wording if needed to reflect exact budgeting rather than fixed tail-only trimming.
- [x] Add runner-level tests that simulate cloud overflow followed by local retry and assert the tool-loop input carries the tight-context flag.

## Phase 4 — Add a conservative static local-fallback tool profile

- [x] Define an explicit compact fallback tool allowlist/profile. Initial candidates:
  - read/search/list tools: `Read`, `Grep`, `Glob`, `LS`, `stat_file`, possibly `ViewImage`/`inspect_image`,
                                                                                                                                            - edit tools: `Edit`, `Write` only when the active permission profile allows writes,
                                                                                                                                            - execution: `Bash` only when the active permission profile allows execution,
                                                                                                                                            - git inspection/checkpoint tools that are already permission-gated,
                                                                                                                                            - planning/checkpoint handoff tools needed by Cercano's own workflow,
                                                                                                                                            - the hydration tool(s) from Phase 5.
- [x] Implement the profile as a real capability profile or allow predicate, not as ad hoc string filtering in the request builder.
- [x] Combine the compact profile with the active permission profile by intersection. Compact fallback can remove advertised tools; it must never add a tool the active profile forbids.
- [x] Log catalog size and tool names in compact mode, using existing truncation helpers where needed.
- [x] Tests:
  - [x] compact mode advertises fewer tools than normal mode,
  - [x] permission restrictions still win,
  - [x] normal cloud/frontier mode still advertises the current catalog.

## Phase 5 — Add hydrated tool catalog support

- [x] Add a small native hydration tool, name to be finalized during implementation, with behavior like `enable_tools` or `get_tool_info`.
- [x] Provide a terse directory of non-advertised tools in tight-context mode. Directory entries should include only name, category, one-line purpose, and permission tier.
- [x] Store hydrated tool names in the current tool-loop state for the lifetime of the turn.
- [x] After a hydration call, rebuild the catalog for the next model iteration to include the full schemas for the requested allowed tools.
- [x] Deny hydration for unknown tools or tools blocked by the active permission profile with a clear tool-result message.
- [x] Make hydration idempotent: requesting an already-enabled tool should succeed without duplicating catalog entries.
- [x] Tests:
  - [x] allowed tool hydration adds its schema on the next request,
  - [x] denied tool hydration does not add the schema,
  - [x] duplicate hydration is harmless,
  - [x] terse directory is present only in compact/tight-context mode.

## Phase 6 — Verification and live diagnostics

- [x] Run focused tests for touched packages first, likely:
  - [x] `go test ./internal/agent`
  - [x] `go test ./internal/agenttools`
  - [x] `go test ./internal/runner`
  - [x] any new package tests for the budget helper.
- [x] Run broader server verification if shared interfaces changed:
  - [x] `go test ./...`
- [x] Exercise a synthetic local 32k-window request with many tools and long history. Confirm logs show:
  - [x] compact/tight-context mode,
  - [x] reduced catalog size,
  - [x] tool schema token estimate,
  - [x] trimmed history count,
  - [x] final estimated prompt below budget.
- [x] Inspect the failure path by forcing an unfit minimal request and confirm it returns preflight `context_overflow` without contacting the provider.

## Sequencing notes

- Phases 1 and 2 are the critical correctness fix. They stop local fallback from making a known-oversized provider request.
- Phase 4 is the quality fix for local fallback: without it, the budgeter may have to discard too much useful conversation history to pay for rarely used tool schemas.
- Phase 5 is valuable but more structural. If implementation risk grows, land Phases 1-4 first and keep hydration as a follow-up checkpoint under the same effort.
- Avoid a generic `call_tool` router unless native tool schemas prove impossible to budget. The current preferred design keeps native tool calling for actual tools after hydration.
