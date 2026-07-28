# HANDOFF — start here (fresh conversation)

You are picking up the `runtime-aware-model-tiers` effort mid-stream. Phase 1 is
committed and green. Read this whole file, then read `spec.md` and `plan.md` in
this directory before editing anything.

Repo root: `/Users/bryancostanich/git_repos/bryan_costanich/Cercano`
Server module: `source/server` (run `go build ./...` / `go test ./...` from there)
CLI module: `source/clients/cli`

## RE-SPECCED — the old "string→map" task below is SUPERSEDED

The effort was re-scoped after discovering the real defect: `ModelTier{Cloud,
Open}` fuses two unrelated taxonomies. The current design (authoritative in
`spec.md`/`plan.md`) is:

1. **Delete the retired four-tier cloud residue** (`ModelTier.Cloud`, the
   `cloud:` block in `tier_recommendations.yaml`, the `.cloud` patch/show
   plumbing). Cloud already has a correct, live, vendor-keyed cost-tier path
   (`ModelProfiles.Cloud.Providers` + `ResolveCloudModelForTier`, three tiers:
   economy/standard/premium) — that stays untouched. Do NOT confuse
   `ModelTier.Cloud` (retired string slot, delete) with `inference.Tiers.Cloud`
   (live cloud TurnRunner, keep).
2. **Re-key the open side as per-runtime OVERRIDES** (not full sets). Each
   runtime's curated `catalog.json` IS the default. Config stores only the tiers
   the user changed, keyed by runtime: `models.open.overrides.<runtime>.<tier> =
   model-id`. Lazy (nothing stored until customized), per-runtime, never ported.
   Tier entries are **model-id strings only — no settings** (recon confirmed
   launch settings are run-host, already per-runtime in `LlamaServerConfig`/
   `MistralRSConfig`). Resolution = **override else catalog default**, merged by
   the SERVER (merge locus A) so `pkg/config` never imports the catalog.
   `ModelTier` is deleted.

STATUS: Phase 2 DONE (cloud residue deleted, resolver collapsed to
`ResolveOpen`, default_provider + proto `*_cloud` fields retired — commits
1abd259f, 938a5bfb). Spec/plan re-revised for the override model after settling
the design with the user (model-id-only, lazy overrides, server merge). Phase 3
is next.

Phases: Phase 3 = re-key open config as per-runtime overrides (pkg/config only);
4 = worker wire; 5 = server merge (locus A) + switch/watcher; 6 = CLI setup +
customization; 7 = verify. See `plan.md` for the grounded, settled detail.

**No migration — clean break** (user-approved; existing flat `open:` values and
`tiers.*.cloud` values load-ignore; catalog default fills in).

## (Superseded) original one-line task

~~Change `ModelTier.Open` from a single `string` to a per-runtime map~~ — this
described only half the fix and kept the fused struct. Replaced by the
two-taxonomy split above.

## Why (context from Phase 0 recon — already verified, do not re-litigate)

- The runtime × RAM × tier matrix ALREADY EXISTS in
  `source/server/internal/localruntime/{mistralrs,llamaserver}/catalog.json`
  (profiles keyed by RAM bucket, `ProfileForRAM(bytes)`, `SupportsTools`,
  and now `PlainChatOK`/`Status` from Phase 1). Do NOT rebuild the matrix.
- `server.recommendedOpenModels(ram)` (server.go ~1969) already switches on the
  active runtime and returns tier→model for that runtime+RAM. Use it.
- The defect is only that `ModelTier.Open` is a single string, so the persisted/
  resolved tier is not runtime-aware.

## Verified capability facts (encoded in Phase 1; rely on them)

- qwen GGUF on llama-server: fully works (tools + plain chat). Preferred default.
- GLM-4.5-Air GGUF on llama-server: loads, tool calls + tool-result history work,
  but PLAIN CHAT RETURNS EMPTY. Flagged `plain_chat_ok:false`, never a plain-chat
  default. (llama catalog `128` profile most_capable was repointed off GLM.)
- qwen UQFF on mistral.rs: loads but multi-turn tool use is broken.
- GLM/Phi GGUF on mistral.rs: do not load on the pinned build.

## Design (approved B + D)

```go
type ModelProfile struct { // catalog-level; capability data lives here, not in user config
    ID, Runtime, Model string
    MinRAMGB int
    SupportsTools, PlainChatOK bool
    Status string
}
type ModelTier struct {
    Cloud string            `yaml:"cloud"`
    Open  map[string]string `yaml:"open"` // runtime -> profileID/model id
}
```

Resolution reads the active runtime: `id := tier.Open[cfg.OpenRuntime]`, then the
per-runtime catalog resolves capabilities. Switching runtime is lossless.

**Key hard part:** `ModelsConfig.Resolve()` and `ModelTier.side()` currently have
NO runtime context. You must thread the active `OpenRuntime` (from `Config`,
`config.go:243`) into open-side resolution. Decide the cleanest threading (pass
runtime into `Resolve`/`side`, or resolve at the `Config` level). Cloud side is
unchanged.

## Exact blast radius (verified — grep-confirmed this session)

`source/server/pkg/config/models.go`
- `ModelTier` struct (~line 39): `Open string` -> `Open map[string]string`
- `OpenChatModel()` (~70), `OpenEmbeddingModel()` (~76): read by active runtime
- `side()` (~101): needs runtime arg
- `ApplyModelTierPatch()` (~158): key shape for setting a runtime's open slot
  (e.g. `everyday.open.llama_server` or keep `everyday.open` meaning active runtime)
- `TierSlots()` (~178): read-side map view
- `Resolve()` (~185+): thread runtime

`source/server/pkg/config/config.go`
- Defaulting at 761–779 (`finalizeModelTiers` + legacy `open_model`/`embedding_model`
  copy): replace with per-runtime matrix defaults; delete legacy copy.
- `applyEnv`/`ApplyKV` open writes at 956, 959, 968.
- Legacy migration test: `pkg/config/models_migrate_test.go` (delete/rewrite; the
  migrate .go file is already gone — only the test remains).

`source/server/pkg/config/tierrecs.go` (67, 102): the flat `open:` block in
`tier_recommendations.yaml` is now redundant vs catalogs — retire or reduce to a
non-open fallback (see spec gap #3).

`source/server/internal/worker/wire.go` (295–303, 388–391): flat
`TierEverydayOpen` etc. cross gRPC. The worker only needs the RESOLVED open model
for the ACTIVE runtime, so wire that (don't ship the whole map) — confirm during
impl.

`source/server/internal/server/server.go`
- 286–288: `rebindOpenTiersForRuntime` sets Everyday/FastLight/FastLightText.Open
  from a single model — make it write the active runtime's slot / pull from
  `recommendedOpenModels(ram)`.
- 752, 1323, 1358: open reads/writes.
- `resolveTierModel` and `UpdateConfig` open-runtime branch (~944–996).

`source/server/internal/server/config_watcher.go` (101–102, 172): `Everyday.Open`/
`Embedding.Open` change detection for the map.

Tests referencing `.Open` as a string (update): `pkg/config/models_test.go`,
`pkg/config/models_migrate_test.go`, plus server/worker tests that build
`ModelTier{Open: ...}`.

## Execution order (from plan.md Phase 2→7)

1. Schema: `ModelTier.Open` -> map; thread runtime; delete legacy migration;
   default from matrix. Keep `pkg/config` tests green first.
2. Worker wire protocol.
3. Server switch (`rebindOpenTiersForRuntime`) + config watcher.
4. Setup + runtime-switch pick from matrix by RAM; GLM never plain-chat default.
5. CLI UI: show only profiles valid for active runtime+RAM.
6. Full `go build ./...` + `go test ./...` for server AND cli; live smoke:
   switch runtime, confirm tiers resolve to that runtime's models, no stale
   cross-runtime values.

## Gates (do not skip)

- Each phase: `go build ./...` and `go test ./...` GREEN before the next.
- No migration code for the legacy flat `open:` string remains at the end.
- Checkpoint after each phase (conventional commit). Do not push unless asked.
- Update `plan.md` checkboxes as you complete tasks.

## Runtime state note

Live config currently has `open_runtime: llama_server`. A GLM llama-server
sidecar and possibly a mistralrs instance may be running from this session's
experiments; the runtime manager (`StopRuntimeModel` RPC) is the clean way to
stop a runtime — do not kill children directly (supervisor respawns them).
