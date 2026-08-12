# Plan: vision as a tool

Build the agentic Air+vision architecture in phases. Each phase keeps
`source/server` build/vet/test green before proceeding.

## Phase 1 — Recon confirmation and seam mapping

- [x] Confirm the exact request path from user images to `llm.Message` blocks
      (`agent.UserInput.Images`, `buildUserBlocks`, runner/tool loop seams).
      Confirmed: gRPC `proto.InlineImage` → `server.mapInlineImages`
      (`server.go:3051`) → `runnersvc.Request.Images` (`server.go:2662`) →
      `runner.runLoop` passes `Images` into `agent.RunToolLoop` (`core.go:278`)
      → `agent.buildUserBlocks` converts markers into `llm.BlockImage`
      (`user_blocks.go:35`) and `inference_turnrunner` has a parallel
      non-tool-loop path (`inference_turnrunner.go:49`).
- [x] Confirm built-in tool registration seam in `internal/capabilities/builtins`
      and execution seam in `internal/agent/toolloop.go` / `agenttools`.
      Confirmed: built-ins register from `builtins.Register` in
      `internal/capabilities/builtins/builtins.go`; each tool implements
      `capabilities.Capability` and is adapted to the agent tool interface via
      the capability registry. Execution flows through `agent.RunToolLoop`,
      which looks up `in.Registry.Get(tc.ToolName)`, partitions by permission,
      executes tools, and converts `agenttools.Result` to tool-result blocks in
      `agent/toolloop.go`.
- [x] Confirm model-tier resolver shape in `pkg/config` and where `everyday`
      tier resolution occurs for local/open providers.
      Confirmed: tiers live in `pkg/config/models.go` (`TierEveryday`,
      `TierFastLight`, `TierFastLightText`, `TierEmbedding`); `validTier` and
      `ApplyModelTierPatch` must learn `TierVision`. Open resolution is
      centralized in `internal/openmodels.Resolver.Model(t)`; server open tier
      resolution calls `s.resolveTierModel(t)` / `DispatchModelFor`, while main
      chat uses `providers.MainModel(false)` → `openModels.ChatModel()`.
      Catalog defaults must also include `vision` before missing overrides can
      fall back cleanly.
- [x] Confirm locus-mode API/structs and the existing cloud/open routing code so
      vision fallback can obey current locus instead of inventing new policy.
      Confirmed: `internal/locus` is the source of truth (`CloudOnly`,
      `CloudPrimary`, `OpenPrimary`, `OpenOnly`). `Mode.Main()` returns
      preferred/fallback/crossing policy used by `hostsvc/providers.Main()` and
      runner cross-tier fallback. For `inspect_image`, local/open vision is
      tried first by design; cloud fallback should be allowed for all modes
      except `OpenOnly` (legacy `local_only` normalizes to `open_only` in
      config load). Use `locus.ParseMode(cfg.LocusMode)` rather than new policy
      strings.
- [x] Record any local surprises in this plan before implementing.
      Local surprises recorded during Phase 1: (1) in this tool schema the
      semantic `plan_set_status` helper is unavailable, so status glyphs are
      maintained by direct Markdown edits during this session; (2) the built-in
      registration seam is `internal/capabilities/builtins/builtins.go`, not a
      `register.go`; (3) cloud fallback for vision should use the locus package
      but the exact predicate is `mode != OpenOnly`, because the vision tool
      always tries local/open first even in cloud-primary/cloud-only modes.

## Phase 2 — Attachment store and placeholder rewriting

Add the per-conversation in-memory image store and make the reasoning model see
explicit placeholders instead of raw image blocks.

- [x] Add an `ImageAttachmentStore` (or equivalent) that stores image bytes,
      media type, hash, ordinal, and generated image ID per live conversation.
      Done: `internal/visionattach.Store` — per-conversation, in-memory, deduped
      by content hash, conversation-scoped IDs `img_<hash6>_<ord>`, caps + Clear.
- [x] Add guardrails: max images per conversation and max total image bytes.
      V1 behavior for cap exceedance: do not store the image; replace it with a
      clear placeholder saying the image was omitted because the live attachment
      limit was exceeded. Done: caps in `visionattach.Store` + omitted
      placeholder in `agent.placeholderFor`.
- [x] Generate conversation-unique image IDs, preferably
      `img_<short-content-hash>_<ordinal>`. Done: `img_<hash6>_<ord>`.
- [x] Rewrite user `BlockImage` entries into text placeholders before the
      reasoning model call:
      `[Image img_... attached for this live conversation. Use inspect_image(...)]`
      Done: `agent.RewriteImagesToPlaceholders` (nil-safe; NOT yet wired into
      the live request path — wiring lands with the tool/tier so the feature
      never goes half-active).
- [x] Ensure raw image blocks are not sent to GLM-4.5-Air / text-only reasoning
      model in the vision-tool path. Done at the rewriter level (no image block
      survives); live wiring deferred as above.
- [x] Tests:
      - one image produces one attachment + one placeholder;
      - multiple images get unique IDs;
      - duplicate image bytes do not collide incorrectly;
      - cap exceedance produces clear placeholder and no stored attachment;
      - rewritten messages preserve surrounding text order.
      Done: `visionattach/store_test.go` + `agent/vision_placeholder_test.go`.
- [x] Build + vet + full `go test ./...` green.
- [x] Checkpoint.

## Phase 3 — First-class `vision` tier/profile

Make vision model selection semantic rather than a one-off config key.

- [x] Add `vision` to the model tier/profile definitions in `pkg/config`.
      Done: `config.TierVision = "vision"` added to the `Tier` enum with a doc
      comment marking it OPTIONAL and override-only (not in any runtime's
      `requiredTiers`); `validTier` and `ApplyModelTierPatch`'s error message
      now recognize it.
- [x] Extend config parsing/defaults/tests so overrides like this are valid:

      ```yaml
      models:
        open:
          overrides:
            llama_server:
              everyday: glm-4.5-air-q4_k_m
              vision: gemma-3-4b-it-q4_k_m
      ```

      Done: the override plumbing is generic per (runtime, tier), so this
      round-trips with no new struct — `SetOverride`/`OverrideFor`/YAML all
      work. `tierrecs.validate` accepts vision as a known tier without
      requiring it (only the four chat tiers are required). No new default is
      seeded yet — curated per-RAM vision defaults land in Phase 9.
- [x] Add a resolver API for "vision tier for current locus/runtime" that the
      tool can call without knowing config internals.
      Done: `openmodels.Resolver.VisionModel() (id string, ok bool)` — override
      then catalog default, with `ok=false` as the normal "no vision model
      configured" condition (not an error) that the tool reads to report
      vision unavailable.
- [x] Ensure no existing tier behavior changes for `everyday`, `fast`, etc.
      Done: only additive changes; `TestVisionModel_DoesNotDisturbEveryday`
      guards everyday resolution, and the full existing config/openmodels test
      suites pass unchanged.
- [x] Tests:
      - `vision` override parses and resolves;
      - missing `vision` override returns a typed/not-found condition;
      - existing tier tests unchanged.
      Done: `config.TestVisionTierOverride` (valid target, round-trip, unset
      miss) and `openmodels` resolver tests (override, catalog default,
      unconfigured→ok=false, everyday-undisturbed).
- [x] Build + vet + full `go test ./...` green.
- [x] Checkpoint.

## Phase 4 — Built-in `inspect_image` tool skeleton

Register the tool and wire it to the attachment store, returning graceful stub
results before real provider calls land.

- [x] Add built-in tool metadata/schema for `inspect_image`:
      - `image_id` string, required;
      - `question` string, required.
      Done: `capabilities/builtins/inspect_image.go`, R-tier, agent-surface only
      (the attachment store is a live agent-session concept — the MCP host has
      no such store), registered in `builtins.Register` (count 40→41).
- [x] Register it only when the active turn/conversation has image attachments,
      or always register it for image-placeholder turns. Avoid prompt/tool list
      mismatch.
      Decision: always register (static catalog); when no vision is
      configured/available the tool returns a clear "vision not available"
      result. This keeps the tool catalog stable across turns (no
      prompt/tool-list churn) and gives the model an honest answer rather than a
      missing tool. Conditional registration is a possible later refinement.
- [x] Implement lookup against the attachment store.
      Done via a `capabilities.VisionService` seam on `Services` (Available /
      Lookup / Inspect), so the tool stays free of a visionattach/inference
      import. The server wires the real implementation over the per-conversation
      store + resolved vision provider in a later phase.
- [x] Return clear unavailable/stale results for:
      - unknown image ID;
      - image evicted/cleared (resume-like behavior);
      - invalid/empty question.
      Done: nil/unavailable service → "Vision is not available…"; stale/unknown
      id → "…no longer available in memory. Ask the user to reattach…"; blank
      id/question → hard arg error (never reaches the model as a fake answer);
      provider error → graceful "Could not inspect image…" result.
- [x] Tests:
      - tool appears for image-placeholder turns;
      - unknown/stale image ID returns reattach message, not hard crash;
      - invalid args return clear tool error/result.
      Done: `inspect_image_test.go` (metadata/surface, success envelope,
      empty-confidence/source omission, nil+unavailable service, stale id,
      provider error, invalid-args table) with a controllable `fakeVision`.
      Builtins count guard bumped to 41.
- [x] Build + vet + full `go test ./...` green.
- [x] Checkpoint.

## Phase 5 — Vision provider call, tool-less and timeout-bound

Make `inspect_image` actually ask the configured vision model.

- [x] Add a small `VisionInspector` interface so tests can inject fake vision
      providers without launching llama-server.
      Done: `capabilities.VisionService` (from Phase 4) is the tool-facing seam;
      `visioninspect.Resolver func() (Resolved, bool)` is the injection point so
      tests supply a fake `inference.Provider` + model with no llama-server.
- [x] Implement real inspector:
      - build a one-turn request with the image block + focused question;
      - target the resolved `vision` tier provider/model;
      - expose **no tools** to the vision model;
      - apply a tight timeout;
      - normalize errors into tool results when appropriate.
      Done: `internal/visioninspect.Inspector` implements `VisionService`. It
      builds a single user message (question text + base64 image block) with a
      steering system prompt, NO tools, MaxTokens cap, and a 90s default timeout
      (overridable). Provider/model come from the injected `Resolver`. Errors
      are returned to the tool, which renders them as graceful tool results
      (not turn-aborting) — the tool owns result normalization; the inspector
      owns the call. This package imports visionattach+inference+llm but NOT
      capabilities, so capabilities stays free of an inference dependency.
- [x] Wrap the answer in the stable text envelope:

      ```text
      Image img_... inspection result:
      Question: ...
      Answer: ...
      Confidence: ...
      Source: ...
      ```

      Done: the envelope is rendered by the tool (`inspect_image.renderVisionEnvelope`
      from Phase 4); the inspector returns `VisionAnswer{Answer, Source}` (Source
      = `provider:model`). Confidence stays optional/blank in V1 and is omitted
      from the envelope when empty.
- [x] Tests:
      - successful local vision call returns envelope;
      - tool-less request: fake inspector asserts no tool schemas are passed;
      - timeout returns a clear tool result;
      - provider error returns a clear tool result and does not abort the whole
        reasoning turn unless the tool framework requires hard errors.
      Done: `visioninspect_test.go` (available/lookup, success+source,
      tool-less request assertion, stale-image guard, no-model, provider error,
      empty answer, prompt timeout, multi-text-block join) with a fake
      `inference.Provider`. Envelope/graceful-result behavior is covered by the
      Phase-4 `inspect_image_test.go`.
- [x] Build + vet + full `go test ./...` green.
- [x] Checkpoint.

Note: the `Resolver` returns a single vision target today. Phase 7 makes that
resolution locus-aware (local/open vision first, then cloud fallback when the
locus permits) by supplying a locus-aware resolver — no change to the inspector.

## Phase 6 — Per-conversation inspection cache

Add the non-persistent cache and resume/stale guards.

- [x] Cache successful results by `(image_id, normalized_question)` within the
      live conversation store.
      Done: `visioninspect.CachingInspector` — a decorator that also implements
      `capabilities.VisionService`, wrapping the real inspector. Per-conversation
      in-memory map keyed by `(image_id, normalized question)`. Available and
      Lookup pass straight through so cache state never shadows a real
      availability/presence check.
- [x] Normalize questions conservatively (trim + collapse whitespace + lower for
      cache key; preserve original question in result envelope).
      Done: `cacheKey` = `image_id \x00 lower(collapse-whitespace(question))`.
      The original question is preserved by the tool's envelope; only the key is
      normalized.
- [x] Cache only successful inspection results, not unavailable/stale errors.
      Done: errors are returned as-is and never stored, so a transient failure
      or a since-reattached image is always retried.
- [x] Tests:
      - repeated same question hits cache and calls fake vision provider once;
      - cache cleared but attachment present re-calls provider successfully;
      - attachment store cleared returns "image no longer available; reattach";
      - two conversations do not share attachments or cache entries;
      - max image/byte caps do not leave dangling cache entries.
      Done: `cache_test.go` covers hit-on-repeat, question normalization,
      distinct-image/question misses, errors-not-cached (retry after fix),
      Clear-forces-recall, per-conversation isolation, Available/Lookup
      pass-through, and nil-inner. The "store cleared → reattach" path is the
      tool+store integration (Lookup passes through to the store, so a cleared
      store makes the tool return the reattach message before Inspect runs);
      that behavior is covered by Phase-4 `inspect_image_test.go` stale-id.
- [x] Build + vet + full `go test ./...` green.
- [x] Checkpoint.

## Phase 7 — Locus-aware fallback

Implement local/open vision first, then cloud fallback only when the current
locus allows it.

- [x] Identify the canonical locus-mode source and predicates (for example:
      local/open-only vs cloud-allowed). Do not invent a parallel policy.
      Done: uses `internal/locus.Mode`. Vision-as-tool policy is local-first
      EVERYWHERE (a vision question is cheap grunt work that stays local even
      under cloud_primary/cloud_only), with cloud fallback allowed for every
      mode except `open_only`. This is deliberately not `Mode.Main()/Coproc()`
      (those prefer cloud under cloud_primary); the only thing the mode governs
      here is whether cloud fallback is permitted at all — predicate is
      `mode != OpenOnly`, matching the Phase-1 recon note.
- [x] Tool resolution order:
      1. configured local/open `vision` tier;
      2. if unavailable/failing and locus allows cloud: active cloud vision provider;
      3. otherwise: unavailable tool result.
      Done: `visioninspect.LocusInspector` (implements `capabilities.VisionService`).
      Inspect tries local, then cloud when permitted+configured — cloud is
      reached both when local is UNAVAILABLE and when a local call FAILS at
      request time (a hung/erroring local model must not strand a turn if cloud
      is allowed). When cloud is not permitted/configured the local outcome
      (including its error) stands. Mode is read live per call.
- [x] Label cloud fallback results honestly in the envelope/source.
      Done: the answering side's `VisionAnswer.Source` (provider:model) rides
      through unchanged, so a cloud fallback envelope names the cloud source.
- [x] Tests:
      - local vision success: cloud not called;
      - local vision unavailable + cloud allowed: cloud called;
      - local vision unavailable + local/open-only: unavailable result, cloud not called;
      - local vision failure + cloud allowed: cloud called;
      - local vision failure + local/open-only: unavailable result;
      - cloud fallback envelope includes source/provider label.
      Done: `locus_test.go` covers all six cases plus an Available() table
      across modes and nil-side wiring (cloud-only, both-nil).
- [x] Build + vet + full `go test ./...` green.
- [x] Checkpoint.

## Phase 8 — End-to-end proof with lightweight vision model

Use a small vision model, not GLM-4.5V, for the target architecture.

> Status (components complete, live wiring pending): Phases 2–7 built every
> vision-as-tool building block in isolation, each unit-tested and green:
> attachment store, placeholder rewriter, `vision` tier + resolver,
> `inspect_image` tool, real tool-less inspector, per-conversation cache, and
> locus-aware local→cloud fallback. NONE are wired into the live request path
> yet — by design, so the feature never goes half-active. Phase 8 does the
> wiring AND the live proof together:
>
> Remaining wiring (code, unit-testable):
> 1. [DONE] Thread a `capabilities.VisionService` into `toolstack.CapDeps` and
>    set it on `capabilities.Services.Vision` in `toolstack.InstallCapabilities`
>    (`internal/toolstack/toolstack.go` is the single host+worker seam). Added
>    the optional `CapDeps.Vision` field (nil default = no behavior change) and a
>    `TestInstallCapabilities_WiresVision` test proving a supplied service reaches
>    `Services().Vision` and stays nil when omitted. The two callers
>    (`server.InstallCapabilities`, `worker_dispatch`) can now pass a live
>    inspector without another toolstack change.
> 2. Construct the live service at the host and worker call sites:
>    `visioninspect.NewCaching(visioninspect.NewLocus(localInspector, cloudInspector, modeFn))`,
>    where each side is `visioninspect.New(store, resolver)` — `store` is one
>    shared `visionattach.Store`, the local resolver yields the open provider +
>    `openModels.VisionModel()`, the cloud resolver yields the cloud provider +
>    cloud vision model, and `modeFn` reads `locus.ParseMode(cfg.LocusMode)`.
> 3. Call `agent.RewriteImagesToPlaceholders(store, convID, msgs)` in the live
>    request path (the image→`llm.BlockImage` seam identified in Phase 1) so the
>    reasoning model gets placeholders and the store is populated, guarded so it
>    is a no-op when no vision service is configured (nil store).
>
> Live proof (requires running agent + a real vision GGUF + mmproj, per below).

- [ ] Select and add/download one lightweight vision model if none is already
      configured for the `vision` tier. Candidate families:
      - Gemma-3-4B vision GGUF + mmproj;
      - Qwen2.5-VL 3B/7B GGUF + mmproj;
      - SmolVLM / Moondream-class models.
- [ ] Configure:

      ```yaml
      models.open.overrides.llama_server.everyday: glm-4.5-air-q4_k_m
      models.open.overrides.llama_server.vision: <lightweight-vision-model>
      ```

- [ ] End-to-end turn:
      - attach screenshot/image;
      - Air sees placeholder;
      - Air calls `inspect_image` with focused question;
      - vision model answers;
      - Air produces final answer using the tool result.
- [ ] Negative end-to-end:
      - no local vision + open-only locus => unavailable result, no cloud;
      - no local vision + cloud-allowed locus => cloud fallback if configured.
- [ ] Record transcript and runtime notes in HANDOFF.md.
- [ ] Build + vet + full `go test ./...` green.
- [ ] Checkpoint.

## Phase 9 — Vision-model survey and per-tier default recommendations

Investigate available image/vision models and produce recommended `vision`-tier
defaults for each RAM/model tier, mirroring how the text tiers already have
per-RAM recommended defaults. This makes vision a real first-class tier in the
default model setup, not a manually-configured extra.

- [ ] Survey candidate vision GGUF + mmproj families verified to load on the
      pinned llama.cpp build. At minimum evaluate: SmolVLM / Moondream-class
      (low RAM), Gemma-3-4B vision, Qwen2.5-VL 3B/7B, and GLM-4.5V (high RAM,
      already downloaded). Capture repo, model file, mmproj file, arch, and
      measured/estimated RSS at a representative context.
- [ ] Map each candidate to a RAM/model tier bracket consistent with the
      existing text-tier RAM brackets (the same tiering used for `everyday`),
      so a machine that fits a given text tier gets a sensible `vision` default.
- [ ] Add recommended `vision`-tier defaults into the catalog/default model
      setup so `models.open.overrides.<runtime>.vision` has a curated default
      per tier instead of requiring manual config. Reuse the curated-catalog
      mechanism (`supports_vision`, `mmproj_file`) from the local-model-vision
      effort.
- [ ] Ensure the "fit" / RAM-headroom logic accounts for the vision model
      being loaded IN ADDITION TO the text reasoning model when both are needed,
      or documents the swap/mutual-exclusion policy if they cannot co-reside.
- [ ] Tests: default-vision resolution per tier picks the expected model;
      missing/oversized tiers degrade cleanly (no vision default rather than an
      un-loadable one).
- [ ] Record the survey results and chosen per-tier defaults in HANDOFF.md.
- [ ] Build + vet + full `go test ./...` green.
- [ ] Checkpoint.

## Verification strategy

- Unit-test attachment store, placeholder rewriting, tier resolution, tool schema,
  cache, and fallback policy with fakes.
- Avoid requiring a live llama-server until Phase 8.
- For live tests, never run GLM-4.5-Air and a large vision model together unless
  memory headroom is explicitly verified.
- Prefer a lightweight vision model for Phase 8 so the end-to-end proof reflects
  the intended architecture.

## Risks / notes

- Tool-use quality: GLM-4.5-Air must reliably call `inspect_image` when the
  placeholder tells it to. If it underuses the tool, a future optional pre-caption
  policy can layer on top.
- Resume behavior: because attachments/cache are not persisted in V1, resumed
  conversations can contain stale image IDs. The tool must return a clear
  reattach result rather than crashing or hallucinating.
- Locus policy: use the existing source of truth. Do not add a second hidden
  cloud/local permission system.
- UI model chip: previous GLM-4.5V testing showed the title chip may reflect the
  selected/default model rather than loaded runtime instance. This is a separate
  UI/state issue to chase later, not part of this effort.
