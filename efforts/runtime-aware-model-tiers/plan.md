# Plan: split cloud/open taxonomies; runtime-keyed open tiers

> Re-specced after discovering the real defect: `ModelTier{Cloud, Open}` fuses
> two unrelated taxonomies. See `spec.md`. Phase 1 (catalog capability fields) is
> committed and green and is KEPT. Everything below (the old string→map Phase 2+)
> is replaced by the two-taxonomy design.

No migration. Clean break. Each phase must build and keep the full test suite
green before moving on. Order: delete cloud residue first (small, isolates the
open work), then re-key open runtime-outer, then wiring, then UI.

## Phase 1 — Catalog capability fields + fix bad GLM default (DONE, KEPT)

Committed and green. Lives in the correct per-runtime catalog layer; unaffected
by the taxonomy split.

- [x] `PlainChatOK *bool` (nil=true) + `Status` on `CuratedModel`, both catalogs,
      with `PlainChatSupported()`.
- [x] GLM flagged `plain_chat_ok:false`, `status:"broken"`; llama `128`
      `most_capable` repointed off GLM to a verified qwen.
- [x] Loader rejects any chat (non-embedding) tier referencing a
      `plain_chat_ok:false` model.
- [x] Tests green; full server build + `go test ./...` green.

## Phase 2 — Delete the four-tier cloud residue

Isolate cloud from the open rework by removing the retired capability-tier cloud
slots. The live vendor-keyed cost-tier path (`ModelProfiles.Cloud.Providers`,
`ResolveCloudModelForTier`) is NOT touched.

- [ ] Confirm (one grep pass) that `ModelTier.Cloud` and the `cloud:` block in
      `tier_recommendations.yaml` are read only by config surface / wizard
      autofill, never by live resolution. Record the finding in HANDOFF.md.
- [ ] Remove the `Cloud` field from `ModelTier` (temporarily leaving `Open` as
      today's string so this phase compiles in isolation).
- [ ] Remove the cloud branch from `side()`, the cloud write from
      `ApplyModelTierPatch()`, and the cloud emit from `TierSlots()`. `Resolve()`
      loses its cloud fallback branch (cloud no longer resolves through tiers).
- [ ] Delete the `cloud:` block from `tier_recommendations.yaml` and the
      `r.Cloud` loader/lookup in `tierrecs.go`.
- [ ] Update tests that set/read `.Cloud` (`models_test.go:142`,
      `tierrecs_test.go` cloud cases).
- [ ] `go build ./...` + `go test ./...` green. Checkpoint.

## Phase 3 — Re-key the open side runtime-outer

Replace the fused tier struct with a per-runtime open tier set.

- [ ] New types in `pkg/config/models.go`:
      - `OpenTierSet` — the five open tiers (`most_capable`, `everyday`,
        `fast_light`, `fast_light_text`, `embedding`) as string model ids.
      - `OpenModels{ Runtimes map[string]OpenTierSet }` replacing `ModelTiers`'
        open role; `ModelTier` deleted entirely.
      - Decide where cloud cost tiers already live (`ModelProfiles.Cloud`) — no
        new cloud type needed.
- [ ] Resolution: open reads `open.runtimes[cfg.OpenRuntime][tier]`. Thread the
      active runtime explicitly into `OpenChatModel()` / `OpenEmbeddingModel()`
      (they already hang off `*Config`, so they read `c.OpenRuntime`). No cached
      copy of the runtime inside the models sub-struct (no split-brain).
- [ ] `ApplyModelTierPatch` / `TierSlots` key grammar becomes open-only and
      runtime-explicit: `open.<runtime>.<tier>` for writes, and `TierSlots`
      shows the active runtime's set. (Cloud is configured via its own
      vendor/profile path, not here.)
- [ ] Defaulting/finalize (`config.go` ~761–781): fill a runtime's open tier set
      from that runtime's `catalog.json` by detected RAM, for each known runtime
      (or at least the active one — confirm during impl). Delete the flat-string
      defaulting and the `OpenModel` legacy copy.
- [ ] Delete legacy migration: `models_migrate_test.go` and the flat-`open`
      migration code paths.
- [ ] Rewrite `pkg/config` tests for the runtime-outer shape (no migration
      tests). `go build ./...` + `go test ./pkg/config/...` green. Checkpoint.

## Phase 4 — Worker wire protocol

- [ ] Replace flat `TierEverydayOpen` etc. in `internal/worker/wire.go`. The
      worker only needs the resolved open model for the active runtime, so send
      the resolved-for-active-runtime ids, not the whole map (confirm during
      impl).
- [ ] Update `wire_test.go`. `go test ./internal/worker/...` green.

## Phase 5 — Server switch + config watcher

- [ ] Rework `rebindOpenTiersForRuntime` / the runtime-switch flow: switching
      runtime selects that runtime's open tier set (from config, filled from the
      catalog by RAM if absent) instead of overwriting a single string.
- [ ] Update `config_watcher.go` open-tier change detection for the new shape.
- [ ] Update `ensure_switch_test.go`, `models_resolve_test.go`,
      `embedding_tier_test.go`. `go test ./internal/server/...` green.

## Phase 6 — CLI UI

- [ ] Tier/runtime pickers operate on the active runtime's open tier set and show
      only catalog-valid models for that runtime+RAM; GLM never auto-selected for
      plain-chat/everyday.
- [ ] Update UI tests.

## Phase 7 — Full verification

- [ ] `go build ./...` server + CLI.
- [ ] `go test ./...` server + CLI.
- [ ] Live smoke: switch runtime; confirm the open tiers resolve to that
      runtime's models with no stale cross-runtime values; confirm cloud still
      resolves via the vendor cost-tier path.
