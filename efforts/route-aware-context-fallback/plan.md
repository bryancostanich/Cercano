# Route-Aware Context Fallback

Align Cercano's provider failure handling, concrete attempt attribution, and context assembly so context overflow is handled deliberately instead of cascading into smaller local fallback with misleading status text.

## Phase 1 — Shared fallback policy

Objective: move the failover policy to the shared LLM error layer and make both resilience and runner orchestration call the same helper.
Files: `source/server/internal/llm/error.go`, `source/server/internal/inference/resilience/resilience.go`, `source/server/internal/runner/core.go`, related tests.
Tests: unit tests proving `context_overflow` does not fail over by default, invalid-request only fails over for model-unavailable cases, and runner cross-tier fallback respects the shared policy.

- [x] Add exported LLM-layer fallback policy helper beside `Retryable` and `ClassOf`
- [x] Move or replicate the provider-model-unavailable detection behind that helper without creating a second policy copy
- [x] Update resilience `Chat`, `StreamChat`, and stream reader recovery to use the shared helper
- [x] Update runner-level cross-tier fallback to use the shared helper before considering fallback providers
- [x] Add regression coverage for runner-level `context_overflow` producing no smaller/equal-window cross-tier fallback

## Phase 2 — Concrete attempt metadata and attribution

Objective: make logs, notices, and errors distinguish selected logical route from the concrete provider/model that actually handled the attempt.
Files: `source/server/internal/runner/core.go`, `source/server/internal/inference/resilience/resilience.go`, provider interfaces/types under `source/server/internal/llm/`, routing-log call sites, related tests.
Tests: unit tests or focused fake-provider tests asserting fallback notices and routing-log events name the concrete attempted provider/model and do not mislabel OpenAI Responses failures as Anthropic failures.

- [ ] Identify the smallest attempt metadata struct needed for route label, provider name, model name, tier, and context window
- [ ] Thread attempt metadata through primary, backup, retry, and cross-tier fallback attempts
- [ ] Update routing-log `loop.start`, `loop.result`, `fallback.consider`, and fallback-attempt fields to include selected route and concrete attempted provider/model
- [ ] Update user-visible fallback notices to use concrete attempt metadata or neutral route wording when metadata is unavailable
- [ ] Add tests for provider attribution through wrapped `llm.Error` values and fallback notices

## Phase 3 — Typed streaming errors

Objective: preserve provider-normalized error class and attribution for in-band stream failures before resilience or runner policy decisions are made.
Files: `source/server/internal/llm/`, provider stream adapters under Anthropic/OpenAI-compatible/OpenAI Responses/Ollama as needed, `source/server/internal/inference/resilience/resilience.go`, stream tests.
Tests: stream unit tests showing pre-content `context_overflow` remains `llm.ErrContextOverflow`, surfaces or larger-window-fallbacks according to policy, and is not converted into `ErrUnknown` merely because it arrived in-band.

- [ ] Extend or adapt `llm.StreamEvent` so error events can carry a typed `error` while retaining display text if needed
- [ ] Update provider stream adapters to emit typed normalized errors for in-band stream failures
- [ ] Update resilience stream reader logic to prefer typed event errors over reconstructing errors from text
- [ ] Preserve existing UI/status behavior for non-classified stream notices
- [ ] Add coverage for OpenAI Responses stream context-window errors and generic stream errors

## Phase 4 — Route-aware request assembly

Objective: introduce one request assembly path that sizes conversation history for a concrete provider/model attempt and exposes structured token accounting to both runner and context meter.
Files: likely `source/server/internal/hostsvc/persistence/persistence.go`, new or existing assembly package under `source/server/internal/`, `source/server/internal/runner/core.go`, `source/server/internal/contextmeter/`, tests.
Tests: assembler tests for raw/compacted/elided/truncated/final token accounting; tests proving meter output and actual request assembly agree for the same concrete target.

- [ ] Extract the current history assembly and hard-elision logic into a dedicated request assembly service/path
- [ ] Define the concrete target input: selected route, provider, model, tier, context-window limit, and tight-context mode
- [ ] Return structured accounting for raw, compacted, elided, truncated, and final request token counts
- [ ] Wire runner primary, backup, retry, and fallback attempts to assemble history for their concrete target before sending
- [ ] Wire `GetContextUsage` to the same assembler so the UI meter reflects the next send-view estimate
- [ ] Add routing-log events or fields for per-attempt assembly accounting without logging prompt text, tool arguments, API keys, or response bodies

## Phase 5 — Context-overflow fallback semantics

Objective: allow context-overflow recovery only when a configured fallback target has a larger known context window and can be assembled without silent context loss.
Files: shared fallback policy helper, runner fallback selection, model/context-window lookup helpers, assembler tests, runner/resilience tests.
Tests: no fallback to smaller/equal-window local model; fallback allowed to a larger known-window fake model; unknown-window cases surface safely rather than silently shrinking.

- [ ] Add or reuse model context-window lookup for concrete fallback targets
- [ ] Compare failed attempt window against fallback attempt window before allowing context-overflow fallback
- [ ] Block context-overflow fallback when the fallback window is unknown, smaller, or equal
- [ ] Require route-aware assembly for the fallback target before sending any allowed larger-window fallback request
- [ ] Make blocked context-overflow fallback surface a clear error/notice rather than trying local with trimmed history
- [ ] Add regression tests for smaller/equal/unknown/larger fallback-window cases

## Phase 6 — Verification and cleanup

Objective: validate the behavior with focused tests, remove obsolete duplicate policy code, and leave follow-up notes for live llama-server diagnostics without doing that work here.
Files: touched files from earlier phases, effort notes if needed.
Tests: focused Go package tests for `internal/llm`, `internal/inference/resilience`, runner fallback logic, stream normalization, request assembly, and context usage; no live provider tests required.

- [ ] Run focused unit tests for LLM error policy and stream normalization
- [ ] Run focused resilience tests including streaming pre-content failures
- [ ] Run focused runner tests for cross-tier fallback policy and attribution
- [ ] Run request assembly and context meter consistency tests
- [ ] Run the narrow server package test set covering changed packages
- [ ] Remove any stale duplicated fallback policy helpers
- [ ] Add a short follow-up note or issue stub for live `llama_server` 500 diagnostics if no existing note covers it
- [ ] Summarize implemented behavior, tests run, and any remaining risks before checkpointing
