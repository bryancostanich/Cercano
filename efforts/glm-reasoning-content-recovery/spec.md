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
- Do not add per-model llama-server launch arguments in the *adapter fix* (Phases 1–2). Adding a per-model catalog `ExtraArgs` field is in scope for Phase 4 (see amended Decision 3).
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

### Decision 3 — Do not add per-model launch args *in the adapter fix*; add them in the Phase 4 catalog update

Chosen option: keep the Phase 1–2 adapter fix free of launch-arg changes, but add a **per-model catalog `ExtraArgs` field** in Phase 4 so GLM launches with `--jinja`. Never use global `llama_server.extra_args` for model-specific behavior.

> **Amendment (post-Phase-3):** This decision originally banned per-model launch
> args entirely, on the premise that `--jinja` was "not reliable enough." Phase 3
> live verification **inverted that premise**: GLM's *no-jinja* path is the broken
> one — it compute-fails at decode with `compute status: -1` on every request —
> while the `--jinja` path loads and serves correctly. `--jinja` is therefore not
> an optional reliability tweak but a **mandatory launch flag** for GLM to run at
> all. Because Phase 4 repoints wizard-recommended tiers to GLM, a durable home
> for `--jinja` is required or first-run users get a GLM that cannot serve. The
> only durable, non-hacky home is a per-model `ExtraArgs` field on the catalog
> model (the "right architecture if launch flags become required" this table
> already identified). The global-`extra_args` option remains rejected: it would
> attach `--jinja` to every llama-server model, including Qwen.

| Axis | Do not add launch args at all | Per-model catalog `ExtraArgs` (Phase 4) | Global `llama_server.extra_args` |
|---|---|---|---|
| Cost | Low: no schema change | Medium: add field to `CuratedModel`, thread through `argsFor`, tests | Low |
| Risk | **Ships a broken default**: wizard-recommended GLM compute-fails without `--jinja` | Low: flag scoped to the one model that needs it | High: model-specific flags leak onto Qwen and others |
| Reward | — | GLM launches correctly wherever it is recommended | Quick manual experiment path |
| Side effects | Leaves GLM unrunnable through normal tiers | Adds a correct, reusable per-model launch surface | Encodes GLM-specific behavior globally |
| Best reason | Smallest adapter-only fix | Only durable home for a now-mandatory flag | Fastest experiment |
| Main drawback | Phase 4 cannot ship GLM as a default | Slightly more Phase 4 scope | Breaks unrelated llama-server models |

Rationale: The adapter fix (Phases 1–2) still needs no launch-arg change — it recovers misfiled `content`. But Phase 3 proved GLM cannot even run without `--jinja`, so once Phase 4 routes real tiers to GLM, the flag must travel with the model. A per-model catalog `ExtraArgs` field delivers it durably and scoped to GLM alone; global `extra_args` is still rejected because it would affect Qwen and every other llama-server model.

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
