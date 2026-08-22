# Plan: Tune llama-server context sizes by RAM profile

Effort: `efforts/tune-llama-context-ram-tiers`
Spec: `efforts/tune-llama-context-ram-tiers/spec.md`

## Phase 1 — Catalog profile data model

- [x] Add a `ProfileEntry` type for llama-server catalog profiles, with fields such as `Model string` and `ContextSize int`.
- [x] Implement backward-compatible JSON parsing so existing string entries still parse as `ProfileEntry{Model: <id>, ContextSize: 0}`.
- [x] Update `CuratedCatalog.Profiles` from `map[string]map[string]string` to a profile-entry map while preserving public helpers that return model IDs where needed.
- [x] Update validation to check `ProfileEntry.Model`, not the raw string value.
- [x] Add tests for string profile entries, object profile entries, missing `model`, unknown models, and invalid/non-positive `ctx_size` values.

## Phase 2 — Effective context resolver and launch precedence

- [x] Replace the current model-extra-args-only `EffectiveContextSize` shape with a resolver that accepts explicit user config, profile `ctx_size`, model `extra_args --ctx-size`, and default context.
- [x] Implement precedence:
  - [x] explicit user config `llama_server.context_size`
  - [x] profile `ctx_size`
  - [x] model `extra_args --ctx-size` for legacy/backward compatibility
  - [x] default context size
- [x] Ensure `argsFor` emits exactly one `--ctx-size`, using the resolved effective context.
- [x] Ensure model `ExtraArgs` cannot append a later conflicting `--ctx-size` when user/profile context is active.
- [x] Update tests from the previous context-window fix (`bc2491f6cc5a`) to cover the new precedence and no-duplicate launch args.

## Phase 3 — Wire effective context everywhere

- [x] Update runner tool-loop context-budget resolution to use the new effective context from the selected RAM profile/model.
- [x] Update dispatch/sub-agent preflight context-window resolver to use the same effective context.
- [x] Update llama-server memory guard projection to use the effective context used for launch args.
- [x] Update any UI/catalog detail surfaces if they report max/effective context.
- [x] Add tests proving launch args, runner budget window, sub-agent preflight window, and memory guard projection agree for the same model/profile.

## Phase 4 — Tune catalog profiles

- [x] Remove RAM-tier policy `--ctx-size` from `glm-4.5-air-q4_k_m` model `extra_args`, leaving model-required flags such as `--jinja` and cache-type flags.
- [x] Add explicit profile-level `ctx_size` entries for all llama-server RAM profiles.
- [x] Keep the existing profile thresholds `24`, `48`, `96`, and `128`; do not add a `64` profile.
- [x] Set `128` profile GLM entries to `ctx_size: 131072`.
- [x] Set `96` profile GLM entries to `ctx_size: 65536`.
- [x] Set 48 GB model entries explicitly; proposed starting point is `glm-4.7-flash-q5_k_m` at `ctx_size: 32768` unless KV math shows 64K is safe. This also covers 64 GB machines under the existing selector.
- [x] Set 24 GB model entries explicitly; proposed starting point is `qwen3-14b-q4_k_m` at `ctx_size: 32768`.
- [x] Decide how to represent embedding context (`nomic-embed-text-v1.5-f16`): explicit small `ctx_size` such as 8192, or `0`/omitted if embedding ignores chat context.

## Phase 5 — RAM/KV validation

- [x] Compute available GGUF KV bytes/token for the profile models present locally, without launching any additional llama-server process.
- [x] For models not present locally, use catalog size plus conservative assumptions; record the assumption in the commit body.
- [x] For each profile, calculate weights + KV at the chosen `ctx_size` and compare with memory guard headroom expectations.
- [x] Add or update tests that ensure chosen profile contexts do not exceed model training context where metadata is available.
- [x] Ensure 128K GLM remains within training context `131072`.

## Phase 6 — Verification and deployment

- [x] Run focused tests:
  - [x] `go test ./internal/localruntime/llamaserver`
  - [x] `go test ./internal/runner`
  - [x] relevant server tests for dispatch/preflight integration.
- [x] Run broader server verification:
  - [x] `go test ./...`
- [ ] Checkpoint the work with a commit body including before/after context sizes and the GLM KV math.
- [ ] Rebuild `~/bin/.cercano-libexec/cercano`.
- [ ] Do not restart automatically; tell the user restart is required and let them choose timing.

## Safety constraints

- Do not launch a second llama-server.
- Do not push.
- Do not weaken duplicate-server or memory-guard protections.
- Do not exceed a model's training context.
- Do not rely on cloud fallback or larger cloud context for local profile tuning.
