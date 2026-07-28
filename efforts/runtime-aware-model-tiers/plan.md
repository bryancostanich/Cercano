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

- [x] CONFIRMED (grep pass): `ModelTier.Cloud` is retired residue — only readers
      are `side()`, `ApplyModelTierPatch`, `TierSlots`, and a test. Live cloud
      resolves via the vendor-keyed `ModelProfiles.Cloud.Providers` path, not
      through these slots.
- [x] SCOPE CORRECTION (local surprise): the `cloud:` block in
      `tier_recommendations.yaml` + `tierrecs.go`'s `Cloud` map are NOT residue —
      they are the **setup wizard's cloud autofill** (`Candidates(ProviderCloud,
      provider, tier)`), used live by the CLI wizard at
      `wizard_page.go:241,464`. Those are the correct vendor-keyed wizard picks
      and MUST NOT be deleted. Phase 2 only removes `ModelTier.Cloud`.
- [x] Remove the `Cloud` field from `ModelTier` (left `Open` as a string; open
      re-key is Phase 3).
- [x] BONUS (approved mid-phase — "no more garbage"): the two-sided cross-
      provider resolver was 100% vestigial (every caller passed
      `ProviderOpen, true`). Collapsed it: `Resolve(t, prefer, strict)` →
      `ResolveOpen(t) (string, bool)`; deleted `side()`, `Provider.other()`,
      and the `resolveTierModel` cloud-stuffing hack. Commit 1 (`1abd259f`).
- [x] Retire `default_provider` + the proto `*_cloud`/`default_provider` fields:
      edited `agent.proto` (reserved the field numbers), regenerated
      `agent.pb.go` with protoc v34.1 / protoc-gen-go v1.36.11, removed
      `ModelsConfig.DefaultProvider`, the patch key, wire marshaling, and the
      agentclient field. Commit 2 (`938a5bfb`).
- [x] CLI scope expansion (found mid-phase): `runtime_tiers.go` still listed
      all `.cloud` tier slots + a `default_provider` row in the picker UI —
      the four-tier cloud residue in the CLI. Removed them; open tiers +
      embedding only. Wizard no longer writes `default_provider`.
- [x] Tests updated across both modules; `tierrecs_test.go` cloud cases left
      intact (live wizard autofill, correctly). Loader is non-strict so stale
      `default_provider:`/`tiers.*.cloud:` keys in existing configs load-ignore.
- [x] `go build ./...` + `go test ./...` green for server AND CLI. Two commits.

  NOTE for future: proto regen command (no Makefile target exists) —
  `PATH="$PATH:$(go env GOPATH)/bin" protoc --proto_path=source/proto
  --go_out=. --go_opt=module=cercano/source/server --go-grpc_out=.
  --go-grpc_opt=module=cercano/source/server source/proto/agent.proto`
  writes to repo-root `pkg/proto/` (module= strips the prefix); move the two
  files into `source/server/pkg/proto/`. Verified byte-identical regen.

  NOTE: sub-agent dispatch is currently unreliable — the open `everyday` tier
  routes to GLM-4.5-Air, which Phase 1 flagged `plain_chat_ok:false` (empty
  output). The flag is not yet wired into live tier selection; that is later-
  phase work. Recon/mechanical delegation will misfire until then.

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
