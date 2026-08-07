# GLM Reasoning Content Recovery

## Problem / motivation

GLM-4.5-Air is currently unusable as a plain chat model in Cercano because llama-server can return an empty OpenAI-compatible `content` field while placing the human-readable answer in `reasoning_content`. The answer is not lost, but Cercano currently treats this field as provider reasoning state rather than visible assistant text. The result is an empty assistant response in normal chat and tool-loop flows.

This blocks using GLM as the stronger local model for everyday and agentic dispatch work. It also prevents safely updating the model catalog and user configuration defaults, because the catalog correctly marks GLM as broken for plain chat until the adapter recovers these answers.

## Goals

- Recover visible assistant text when an OpenAI-compatible provider returns empty `content` and non-empty plaintext `reasoning_content`.
- Apply the same recovery rule to both non-streaming and streaming OpenAI-compatible chat paths.
- Preserve existing behavior when normal `content` is present.
- Add focused tests for non-streaming and streaming fallback behavior.
- After adapter verification, explicitly update the GLM catalog/configuration path in a follow-up phase so GLM is no longer left marked or routed as broken by stale defaults.

## Non-goals

- Do not build a general reasoning-display user interface in this effort.
- Do not expose Claude/OpenAI opaque reasoning blobs as visible chat text.
- Do not add per-model llama-server launch arguments in the adapter fix.
- Do not use global `llama_server.extra_args` for GLM-specific behavior.
- Do not change default tier routing until the adapter fix is verified against GLM.

## Constraints

- Existing `BlockReasoning` semantics must remain intact: it is provider-specific state that can be stored and round-tripped, not a general visible-text channel.
- Claude/Anthropic reasoning data is opaque and must not be rendered as user-visible prose.
- GLM `reasoning_content` fallback must only become visible text when `content` is empty; if `content` is present, it remains authoritative.
- Streaming fallback must avoid double-rendering: buffer `reasoning_content`, stream normal `content` as usual, and only emit the buffered reasoning text at stream end if no normal text was emitted.
- Catalog/config changes must be gated on verification that the adapter fix restores GLM visible output.

## Decisions

### Decision 1 — Recover GLM `reasoning_content` as visible text when `content` is empty

Chosen option: promote plaintext `reasoning_content` into a visible text block only when OpenAI-compatible `content` is empty.

| Axis | Promote reasoning → text when content empty | Emit BlockReasoning + teach CLI to render it | Hybrid split on `<think>…</think>` |
|---|---|---|---|
| Cost | Low: small adapter changes and tests | High: adapter plus CLI reasoning-rendering surface | Medium: parsing/splitting logic plus tests |
| Risk | Low: only affects empty-content responses | Medium: risks exposing opaque provider reasoning or raw chain-of-thought | Medium: depends on unreliable tag emission |
| Reward | Restores the visible answer where it is currently misfiled | Could enable a future reasoning UI | Separates answer and reasoning when tags are reliable |
| Side effects | Keeps reasoning UI out of scope | Expands `BlockReasoning` beyond its current opaque-state contract | Adds fragile model-specific parsing |
| Best reason | Matches the proven failure: the answer is plaintext but in the wrong field | Best only if reasoning display is a goal | Attractive only if tags are always present |
| Main drawback | Does not add a separate reasoning display | Too much scope and wrong semantics for opaque reasoning | Hack: works only because tags sometimes appear |

Rationale: Existing `BlockReasoning` is documented and tested as provider-specific opaque state. Claude-style reasoning data is not readable assistant prose. GLM's `reasoning_content` in the failure case is different: it is plaintext and is the actual answer. Routing it through `BlockReasoning` would hide it from the CLI or force a broad reasoning-display feature. Promoting it to visible text only in the empty-content case is the narrow, semantically correct recovery.

### Decision 2 — Fix streaming and non-streaming paths together

Chosen option: implement the fallback in both OpenAI-compatible non-streaming and streaming adapters.

| Axis | Fix non-streaming only | Fix non-streaming + streaming |
|---|---|---|
| Cost | Low: one adapter path | Medium: adapter plus stream reader/tests |
| Risk | Medium: headless tests pass while real TUI streaming remains broken | Low: both paths share the same visible-output contract |
| Reward | Fixes non-stream callers | Fixes real interactive chat, headless callers, and tests |
| Side effects | Creates behavioral mismatch | Slightly more code but simpler runtime behavior |
| Best reason | Fastest patch | Correct user-facing fix |
| Main drawback | Incomplete for the normal CLI path | Requires additional stream tests |

Rationale: The interactive CLI uses streaming, so a non-streaming-only fix would likely miss the primary user-visible failure. The streaming implementation must buffer reasoning deltas and emit them only at stream end if no normal content was emitted.

### Decision 3 — Do not add per-model launch args in this fix

Chosen option: leave llama-server launch arguments unchanged for this adapter fix.

| Axis | Do not add launch args now | Add per-model `extra_args` now | Use global `llama_server.extra_args` |
|---|---|---|---|
| Cost | Low: no runtime/catalog schema change | Medium-high: catalog schema, loader, launch merge, tests | Low |
| Risk | Low: targets the proven adapter failure | Medium: new launch surface can break startup | High: model-specific flags affect unrelated models |
| Reward | Restores visible output without changing launch semantics | Enables future model-specific launch flags | Quick manual experiment path |
| Side effects | Does not improve server-side templating | Adds a correct future extension point | Hack: GLM-specific behavior encoded globally |
| Best reason | Smallest correct fix | Right architecture if launch flags become required | Fastest experiment |
| Main drawback | Adapter remains responsible for recovery | More scope than needed now | Can break Qwen or other llama-server models |

Rationale: `--jinja` was not reliable enough to be the core fix, and the catalog currently has no per-model launch-args field. A per-model launch-args feature may be useful later, but it should not be bundled with this adapter bug fix.

### Decision 4 — Verify first, then update GLM catalog/configuration defaults

Chosen option: ship the adapter fix first; after focused GLM verification, update GLM catalog/configuration in an explicit follow-up phase.

| Axis | Adapter fix only | Adapter fix + mark GLM plain-chat OK | Adapter fix + route high-memory profiles to GLM |
|---|---|---|---|
| Cost | Low | Medium: catalog metadata and guard tests | Medium-high: catalog profiles/bootstrap/default routing |
| Risk | Low | Medium: declares support before fresh verification | Higher: changes first-run/default model selection |
| Reward | Unblocks manual GLM use and proves recovery | Makes catalog status accurate after verification | Makes GLM default for capable local machines |
| Side effects | GLM remains opt-in until follow-up | Catalog validation can accept GLM in chat tiers | Users may load a 73 GB model by default |
| Best reason | Safe narrow fix | Required once GLM is proven fixed | Desired final local-agentic behavior |
| Main drawback | Leaves stale catalog/config state temporarily | Needs verification gate | Too broad to bundle with adapter plumbing |

Rationale: The configuration work must not be forgotten, but it should be gated on proof that the adapter recovery works end-to-end. The execution plan must include an explicit phase after verification to update the GLM catalog status and the relevant config/default routing, rather than leaving it as an informal future idea.
