# Route-Aware Context Fallback

## Problem / motivation

Cercano currently has two overlapping recovery systems for provider failures: the newer provider resilience layer and an older runner-level cross-tier fallback path. The resilience layer already treats context-window overflow as a user-visible sizing problem rather than a reason to fail over, but the runner-level fallback still considers cross-tier fallback for any turn-loop error. In the observed incident, a cloud request that failed with `context_overflow` was treated like a generic provider failure, fell through toward a much smaller local `llama_server` model, and produced confusing warnings and aggressive history trimming.

The incident also showed provider attribution drift. The user-visible warning named the selected wrapper route as `anthropic`, while the concrete failing error text came from `openai-responses`. That makes debugging impossible: the logs and UI must answer which concrete provider and model actually made the failed attempt, not just which logical route was selected.

Finally, context accounting and request assembly are not unified. `GetContextUsage` reports a compacted/elided send-view estimate, but it does not apply the same hard-override truncation and per-attempt model sizing used by actual history assembly. The result is a context meter that can appear safe while a request sent to a concrete provider is much larger or smaller than expected.

## Goals

- Make retry, failover, and cross-tier fallback decisions use one shared provider-failure policy.
- Prevent `context_overflow` from falling through to smaller or equal-window fallback models.
- Allow `context_overflow` recovery only when a configured fallback has a larger known context window and can preserve or improve the send view.
- Attribute every failed attempt to the concrete provider and model that actually handled it.
- Preserve typed stream errors at the provider source rather than collapsing in-band stream failures into bare text.
- Introduce a dedicated request assembly service/path used by both the runner and the context meter.
- Reassemble history for each concrete provider/model attempt so fallback requests are sized for the model that will actually receive them.
- Add routing-log accounting that explains raw, compacted, elided, truncated, and final request token counts per attempt.
- Add regression tests for context-overflow fallback policy, stream error typing, provider attribution, and meter/request assembly consistency.

## Non-goals

- Do not root-cause or fix live `llama_server` HTTP 500 `Compute error.` runtime failures in this effort.
- Do not make the effort depend on a live local model server or local GPU/Metal runtime diagnostics.
- Do not redesign the whole provider selection system beyond the metadata and assembly changes needed for exact attribution and route-aware sizing.
- Do not silently answer from a drastically smaller context as a way to recover from overflow.
- Do not change conversation persistence schema unless implementation proves structured accounting cannot be logged without it.

## Constraints

- Provider and runner behavior must remain testable without live cloud or local model calls.
- The fallback policy must be shared by the resilience layer and runner-level cross-tier fallback; duplicated policy copies are not acceptable.
- User-visible fallback notices must not infer the failed provider from a wrapper route name.
- Routing logs must distinguish selected logical route from concrete attempted provider/model.
- Stream error recovery must operate on typed errors, not only `ErrText`.
- The context meter and actual request assembly must be backed by the same assembly engine or service.
- Context-overflow fallback to a smaller or equal-window model must be blocked.
- Llama-server 500s may be logged/classified better if touched naturally, but deep runtime investigation is out of scope.

## Decisions

### Decision 1: shared fallback policy location

Chosen option: move/promote the fallback policy into `internal/llm`, beside `Retryable` and the other error-class helpers.

Decision: where should the “is another provider worth trying?” policy live?

| Axis | Promote to `internal/llm` | Export from `resilience` | Duplicate in runner |
|---|---|---|---|
| Cost / complexity | Small: move the existing predicate and update the resilience and runner call sites. | Small: export the existing predicate and import resilience from runner. | Small initial edit, but adds a permanent second policy copy. |
| Risk | Low: one policy remains shared and close to error-class vocabulary. | Low/medium: runner begins depending on the resilience engine for a pure policy decision. | Medium/high: the two copies can drift and recreate this bug. |
| Reward / outcome | One source of truth for retry/failover class policy. | One source of truth, but in a less natural package boundary. | Fastest local patch only. |
| Side effects | `llm` owns one more normalization/policy helper, consistent with `Retryable`, `ClassOf`, and related helpers. | Keeps `llm` slightly cleaner but couples orchestration to resilience. | Makes future policy changes harder to audit. |
| Main drawback | The model-unavailable heuristic is string-based and makes `llm` slightly less pure. | Adds a runner-to-resilience dependency. | Known tech-debt option. |

Rationale: the runner already relies on `llm.Retryable` so the retry policy and resilience engine stay in sync. Fallback class policy should follow the same pattern.

### Decision 2: provider attribution

Chosen option: explicit attempt metadata.

Decision: how should retry/fallback notices and logs know who actually failed?

| Axis | Error-provider only | Neutral route wording | Explicit attempt metadata |
|---|---|---|---|
| Cost / complexity | Small: prefer `*llm.Error.Provider`. | Smallest: avoid naming the provider. | Medium: carry selected route plus concrete provider/model through attempts. |
| Risk | Medium: attribution is lost when errors are wrapped or converted to text. | Low for correctness, but loses diagnostic detail. | Low/medium: more plumbing, but exact when implemented consistently. |
| Reward / outcome | Better than wrapper names, still incomplete. | Stops lying but does not answer who failed. | Logs and UI can state the actual attempted provider/model. |
| Side effects | Partial improvement only. | Debuggability decreases. | Enables better routing logs, tests, and future provider diagnostics. |
| Main drawback | Still cannot answer the central question in all paths. | Too vague for debugging. | Broader interface work. |

Rationale: the user explicitly needs to know who actually made the failing request. The implementation must distinguish selected logical route from concrete attempted provider/model.

### Decision 3: stream error typing

Chosen option: convert stream event errors to typed errors at the provider source.

Decision: where should in-band stream error classification happen?

| Axis | Fix only attribution in resilience | Classify `ErrText` in resilience | Typed errors at provider source |
|---|---|---|---|
| Cost / complexity | Small. | Small/medium. | Medium/high: stream event shape and provider adapters need updates. |
| Risk | Leaves context-overflow-as-unknown bugs alive. | Better behavior but still treats provider errors as text downstream. | Cleanest path; risk is broader adapter coverage. |
| Reward / outcome | Better labels only. | Correct decisions for known text patterns. | Preserves class, provider, and model at the source of truth. |
| Side effects | Recovery policy still depends on degraded data. | Resilience becomes a secondary classifier. | Provider adapters become responsible for emitting typed failures. |
| Main drawback | Incomplete fix. | Still papering over a lossy stream API. | More files and tests touched. |

Rationale: recovery policy should not guess from a bare string after the provider has already normalized the failure. `llm.StreamEvent` should carry a typed error, while retaining display text for compatibility if useful.

### Decision 4: backup/fallback request sizing

Chosen option: reassemble history per selected concrete model before each attempt.

Decision: how should Cercano prevent sending a request sized for one model to a smaller fallback model?

| Axis | Pre-check resilience backup only | Pre-check every concrete provider attempt | Reassemble per concrete model |
|---|---|---|---|
| Cost / complexity | Low. | Medium. | High: assembly must become route-aware. |
| Risk | Incomplete for runner-level cross-tier fallback. | Safer, but only rejects oversized attempts. | More moving parts, but models receive correctly sized requests. |
| Reward / outcome | Prevents some backup sends. | Broad safety rail. | Correct behavior and better user experience for each attempt. |
| Side effects | Does not solve meter/request mismatch. | Still leaves separate sizing logic. | Enables unified accounting and truthful meter data. |
| Main drawback | Too narrow for the observed architecture bug. | Rejects rather than adapting. | Larger refactor. |

Rationale: a Claude-sized request should not be reused blindly for OpenAI Responses or local fallback. Each concrete attempt should receive a send view assembled against that concrete model’s context window.

### Decision 5: request assembly boundary

Chosen option: introduce a dedicated request assembly service/path used by runner and meter.

Decision: where should route-aware history assembly live?

| Axis | Runner owns it | Persistence/host service owns it | Dedicated request assembly service |
|---|---|---|---|
| Cost / complexity | Medium. | Medium. | Medium/high. |
| Risk | Runner gains history/compaction mechanics. | Assembly remains coupled to service implementation details. | New boundary must be designed carefully. |
| Reward / outcome | Runner can size attempts directly. | Reuses existing code quickly. | One engine serves real requests and the context meter. |
| Side effects | Meter may drift unless separately wired. | Better than today, but still less explicit. | Makes token accounting an intentional API. |
| Main drawback | Blurs orchestration and assembly responsibilities. | Smaller change but weaker long-term boundary. | More upfront design and tests. |

Rationale: the same code should answer both “what will be sent to provider/model X?” and “what should the UI meter display for provider/model X?” A dedicated assembler prevents another divergence between display and actual request construction.

### Decision 6: context-overflow fallback policy after route-aware assembly

Chosen option: fallback on `context_overflow` only if the fallback has a larger known context window.

Decision: should context overflow ever trigger trying another model?

| Axis | Never fallback | Larger-window fallback only | Always fallback by shrinking |
|---|---|---|---|
| Cost / complexity | Lowest. | Medium. | Medium/high. |
| Risk | Safest but may miss legitimate recovery. | Low/medium: must compare known windows and preserve send-view quality. | High: can silently answer from too little context. |
| Reward / outcome | Predictable surfacing of context errors. | Allows real recovery without smaller-context surprises. | Maximizes chance of some answer. |
| Side effects | No automatic rescue path. | Requires accounting for failed and fallback attempt windows. | Reintroduces the confusing local-with-few-messages behavior. |
| Main drawback | Conservative. | Slightly more policy complexity. | Violates the user expectation from the incident. |

Rationale: if a larger-window model can genuinely hold more context, fallback may solve the overflow. Smaller or equal-window fallback must be blocked because it can only recover by dropping context.

### Decision 7: llama-server 500 scope

Chosen option: do not root-cause live `llama_server` 500 `Compute error.` failures in this effort.

Decision: should this effort fix local llama-server runtime 500s?

| Axis | Guardrail/log only | Root-cause now | Defer entirely |
|---|---|---|---|
| Cost / complexity | Small/medium if naturally touched. | Large/unknown and environment-dependent. | Smallest. |
| Risk | Low. | High: could spiral into local runtime, model loading, Metal/GPU, or server lifecycle work. | Low for this effort, but local failures remain. |
| Reward / outcome | Stops routing cascades from making local failures worse. | Might improve local reliability. | Keeps scope focused on confirmed app bugs. |
| Side effects | Better logs may help later. | Requires live local infrastructure and may be non-deterministic. | Follow-up investigation remains necessary if local reliability matters. |
| Main drawback | Does not explain every 500. | Scope explosion. | Leaves local compute errors unresolved. |

Rationale: the primary bug is that Cercano incorrectly routes/falls back after context overflow and cannot accurately attribute attempts. Live local model 500s are a separate runtime problem and should not block this fix.

## Acceptance criteria

- A context-overflow error from a concrete attempt does not trigger cross-tier fallback to a smaller or equal-window model.
- A context-overflow error may only fallback to a configured concrete model with a larger known context window.
- Fallback notices and routing logs name both the selected logical route and the concrete attempted provider/model.
- In-band streaming provider errors preserve typed error class and concrete provider/model attribution before reaching resilience decisions.
- The context meter and actual request assembly use the same assembly path/accounting.
- Per-attempt routing logs explain raw, compacted, elided, truncated, and final token counts.
- Tests cover runner-level context-overflow no-local-fallback, larger-window overflow fallback, provider attribution, typed stream errors, and meter/request assembly consistency.
