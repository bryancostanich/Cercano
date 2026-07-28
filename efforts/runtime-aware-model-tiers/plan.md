# Plan: runtime-aware, RAM-aware, profile-rich model tiers

No migration. Clean break. Each phase must build and keep the full test suite
green before moving on. Phases are ordered so the schema lands first, then data,
then wiring, then UI.

## Phase 1 — REPLANNED after recon: catalog capability fields + fix bad GLM default

The runtime×RAM×tier matrix already exists in mistralrs/llamaserver catalog.json
with ProfileForRAM + SupportsTools/SupportsEmbed. Do NOT rebuild it. Instead:

- [x] Add `PlainChatOK *bool` (nil=true) and `Status string`
      (tested/experimental/broken) to `CuratedModel` in both llamaserver and
      mistralrs catalogs, with a `PlainChatSupported()` helper.
- [x] Mark `glm-4.5-air-q4_k_m` as `plain_chat_ok:false`, `status:"broken"`.
- [x] Fix the llama `128` profile: `most_capable` was GLM (empty plain chat) →
      repointed to `qwen3-30b-a3b-thinking-2507-q4_k_m`. GLM stays in the model
      dictionary (tool-capable) but is no longer an auto-selected default.
- [x] Loader now rejects any chat (non-embedding) tier referencing a
      `plain_chat_ok:false` model (extracted `validateCatalog`); mirrored in the
      mistralrs catalog loader.
- [x] Tests: new fields parse; GLM flagged; plain-chat tiers gated; loader
      rejection test. Full server build + `go test ./...` green.

## Phase 2 — Schema change: `ModelTier.Open` string -> per-runtime map

- [ ] Change `ModelTier.Open` to `map[string]string` (runtime -> profile/model).
- [ ] Update `side()`, `Resolve()`, `OpenChatModel()`, `ApplyModelTierPatch()`,
      `TierSlots()` to read/write by active `OpenRuntime`.
- [ ] Delete legacy flat-`open` migration (`models_migrate*`, the string
      defaulting in `config.go`). Replace defaulting with matrix-driven stock
      values for the detected runtime+RAM.
- [ ] Tests in `pkg/config` updated for the map shape (no migration tests).

## Phase 3 — Worker wire protocol

- [ ] Replace flat `TierEverydayOpen` etc. in `internal/worker/wire.go` with a
      representation that carries the active runtime's resolved open model (the
      worker only needs the resolved model for the active runtime, not the whole
      map — confirm during implementation).
- [ ] Update `wire_test.go`.

## Phase 4 — Server switch + config watcher

- [ ] Rework `rebindOpenTiersForRuntime` / the switch flow to select the
      runtime's matrix defaults instead of overwriting a single string.
- [ ] Update `config_watcher.go` `Everyday.Open` change detection for the map.
- [ ] Update `ensure_switch_test.go`, `models_resolve_test.go`,
      `embedding_tier_test.go`.

## Phase 5 — Setup + runtime-switch pick from matrix by RAM

- [ ] Setup wizard and runtime-switch choose tier defaults from the matrix using
      detected RAM and target runtime.
- [ ] GLM flagged tool-only: never auto-selected for plain-chat/everyday.

## Phase 6 — CLI UI

- [ ] Tier/runtime pickers show only profiles valid for the active runtime+RAM.
- [ ] Update UI tests.

## Phase 7 — Full verification

- [ ] `go build ./...` server + CLI.
- [ ] `go test ./...` server + CLI.
- [ ] Live smoke: switch runtime, confirm tiers resolve to the runtime's models
      with no stale cross-runtime values.
