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

## Phase 3 — Re-key open config as per-runtime overrides (pkg/config only)

Replace the flat tier struct with runtime-keyed **overrides** (only tiers the
user changed). Tier entries are **model-id strings** — no settings. Defaulting
leaves `pkg/config`; the catalog default is merged by the server in Phase 5.
Keep this phase confined to `pkg/config` so it stays small and verifiable in
isolation.

Grounded decisions (settled with the user; do not relitigate):
- Model-id-only tiers. No per-model/per-tier settings (recon confirmed launch
  settings are run-host, already per-runtime in `LlamaServerConfig`/
  `MistralRSConfig`).
- Overrides, not full sets. Lazy: nothing stored for a runtime until a tier is
  changed. Per-runtime; never ported across runtimes.
- Merge locus A: server merges override-else-catalog-default. `pkg/config` must
  NOT import the catalog.

- [ ] New type in `pkg/config/models.go`: `OpenModels{ Overrides
      map[string]map[string]string }` (`runtime → tier → model-id`), replacing
      `ModelTiers`/`ModelTier` entirely. YAML: `models.open.overrides.<runtime>.
      <tier>`. Keep the five tier constants (`most_capable` … `embedding`).
- [ ] Rewrite resolution to be override-only and !ok-tolerant:
      `ResolveOpen(tier)` reads `Overrides[c.OpenRuntime][tier]` and returns
      `!ok` when there is no override (the server then supplies the catalog
      default — this is expected, not an error). `OpenChatModel()` /
      `OpenEmbeddingModel()` read `c.OpenRuntime` directly (no cached runtime
      copy — no split-brain).
- [ ] `ApplyModelTierPatch` / `TierSlots`: key grammar `open.<runtime>.<tier>`
      (write an override; "-" clears it). `TierSlots` reports the active
      runtime's overrides only. NOTE: patching a tier for a *non-active* runtime
      is allowed (explicit runtime in the key) — this is how setup can seed a
      runtime you're about to switch to.
- [ ] Remove `finalizeModelTiers` tier-defaulting (`config.go` ~761–781) and the
      legacy `open_model→everyday` migration + `CERCANO_OPEN_MODEL`/
      `CERCANO_EMBEDDING_MODEL` writing into the flat struct. Env overrides now
      write the active runtime's override (or defer to Phase 5 — confirm during
      impl which is cleaner). Delete `models_migrate_test.go`.
- [ ] Rewrite `pkg/config` tests for the override shape. Loader stays non-strict
      so stale flat `open:`/`tiers.*` keys load-ignore. `go build ./...` +
      `go test ./pkg/config/...` green. Checkpoint.

## Phase 4 — Worker wire protocol

- [ ] The worker needs the *resolved* open model for the active runtime, not the
      override map. Since resolution now includes the catalog-default merge
      (Phase 5), send the server-resolved active-runtime tier ids over the wire
      (keep the existing flat `TierEverydayOpen` etc. fields, now populated from
      the merged resolution). Confirm no `ModelTiers` struct crosses the wire.
- [ ] Update `wire_test.go`. `go test ./internal/worker/...` green.

## Phase 5 — Server merge (locus A) + switch + config watcher

This is where override-else-catalog-default lives.

- [ ] Add the merge resolver in `internal/server`: `resolveOpenTier(runtime,
      tier) = cfg.Overrides[runtime][tier]  else  catalogDefault(runtime, tier)`,
      where `catalogDefault` reads the runtime's `catalog.json` by detected RAM
      (server may import `internal/localruntime`). Route all open-tier resolution
      through it. Honor Phase 1's `plain_chat_ok:false` (never default-select a
      broken model).
- [ ] `rebindOpenTiersForRuntime` / runtime-switch: reload the active runtime's
      override-over-default set on switch. No stale cross-runtime values.
- [ ] `config_watcher.go`: detect open-override changes in the new shape.
- [ ] Update `ensure_switch_test.go`, `models_resolve_test.go`,
      `embedding_tier_test.go`. `go test ./internal/server/...` green.

## Phase 6 — CLI UI (setup + later customization)

- [ ] Tier picker writes an `open.<active_runtime>.<tier>` override; shows the
      effective value (override if set, else catalog default) and offers only
      catalog-valid models for that runtime+RAM. GLM never auto-selected.
- [ ] Setup wizard seeds overrides for the chosen runtime the same way (same
      override store — setup and later `/config` are one path).
- [ ] Update UI tests.

## Phase 7 — Full verification

- [ ] `go build ./...` server + CLI.
- [ ] `go test ./...` server + CLI.
- [ ] Live smoke: customize a tier (override persists); switch runtime and
      confirm the other runtime resolves its own catalog defaults (no stale
      cross-runtime value); switch back and confirm the override survived;
      confirm an untouched tier tracks the catalog; confirm cloud still resolves
      via the vendor cost-tier path.
