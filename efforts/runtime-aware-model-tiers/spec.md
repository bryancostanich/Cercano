# Split cloud and open into two taxonomies; make the open side runtime-keyed

## The core mistake we are undoing

`config.ModelTier` fused two unrelated taxonomies into one struct:

```go
type ModelTier struct {
    Cloud string `yaml:"cloud"`
    Open  string `yaml:"open"`
}
```

Cloud and open are **not** two columns of one table. They are two different
axes:

- **Cloud** is keyed by **vendor** and has **three cost tiers**
  (economy / standard / premium). A cloud model id belongs to a vendor, not to a
  capability tier, and definitely not to a local runtime.
- **Open** is keyed by **runtime** (`ollama` / `mistralrs` / `llama_server`),
  whose model identifiers are mutually incompatible (Ollama tags vs mistral
  catalog IDs vs llama-server GGUF paths). An open model id is meaningless
  without its runtime.

Forcing both into one `ModelTier{Cloud, Open}` with the four capability tiers
(`most_capable` / `everyday` / `fast_light` / `fast_light_text`) is the original
sin. Every corner this effort kept hitting is that fused struct fighting back:

- The runtime-blind `Open` **string**: switching `open_runtime` leaves a
  now-wrong id (config says one runtime, tiers route to another runtime's
  model). This is the split-brain.
- Cloud jammed into the same four capability tiers, when cloud's real taxonomy
  is three vendor-keyed cost tiers.

## History: cloud was already corrected once, but the residue was left behind

`ef502a4b` already found that resolving cloud through the four capability-keyed
`tiers.*.cloud` slots was wrong (it sent an Anthropic id to Codex, which
rejected it). It introduced the **correct** cloud path — provider-neutral
cost tiers (economy / standard / premium) keyed by vendor, via
`ModelProfiles.Cloud.Providers.<vendor>` and `ResolveCloudModelForTier`, with a
capability→cost bridge (`most_capable→premium`, `everyday→standard`,
`fast_light[_text]→economy`).

That commit **retired** `ModelTier.Cloud` from resolution ("kept load-tolerant,
no longer read") but never deleted it. So today two cloud tier systems coexist:

- **Correct, live**: `ModelProfiles.Cloud.Providers.<vendor>` — three cost
  tiers, vendor-keyed, resolved by `ResolveCloudModelForTier`. **Keep.**
- **Retired residue**: `ModelTier.Cloud`, the `.cloud` slots in
  `tier_recommendations.yaml`, and the `.cloud` patch/show plumbing. This is the
  four-tier cloud that "keeps coming back" — because it was never deleted.

Phase 1 of this effort did **not** touch cloud. The four-tier cloud residue
predates this effort.

## Goal

1. **Delete the four-tier cloud residue** so four-tier cloud is structurally
   impossible: remove `ModelTier.Cloud`, the `cloud:` block in
   `tier_recommendations.yaml`, and the `.cloud` patch/show plumbing. Cloud
   resolution keeps flowing solely through the existing vendor-keyed cost-tier
   path, which is untouched and already correct.

2. **Make the open side runtime-keyed, as lazy per-tier overrides over a
   per-runtime catalog default.** Each runtime has a curated default tier set
   (the existing `catalog.json`). The user's config stores **only the tiers they
   have changed**, keyed by runtime; everything untouched resolves live from the
   catalog default. Switching `open_runtime` selects that runtime's
   overrides-over-default set — never a stale cross-runtime model id, and no
   reconciliation. A tier entry is **just a model id string** (no per-model or
   per-tier settings — run-host settings stay in the per-runtime
   `LlamaServerConfig`/`MistralRSConfig` where they already live).

3. **Keep Phase 1.** Its `plain_chat_ok` / `status` capability fields and the
   build-time gate live in the already-correct per-runtime catalog layer and are
   retained as-is.

## The two taxonomies (target shape)

**Cloud — unchanged, vendor-keyed, three cost tiers:**

```yaml
model_profiles:
  cloud:
    providers:
      anthropic: { economy: claude-haiku, standard: claude-sonnet, premium: claude-opus }
      openai:    { economy: ...,           standard: ...,            premium: ... }
```

Resolved via `ResolveCloudModelForTier(vendor, capabilityTier)` using the
existing capability→cost bridge. The active vendor comes from the active cloud
profile (`CloudProfiles` / `ActiveCloudProfile`). Nothing here changes.

**Open — runtime-keyed, lazy per-tier overrides over the catalog default:**

The **default** for each runtime is the curated `catalog.json` (authored by us,
ships with the binary, read-only). The user's **config stores only overrides** —
the specific tiers they changed, per runtime. Nothing is written for a runtime
until the user customizes a tier of it; untouched tiers are never copied.

```yaml
models:
  open_runtime: llama_server        # active runtime (already cfg.OpenRuntime)
  open:
    overrides:
      llama_server:                 # only present because the user changed it
        everyday: my-custom-qwen    # only tiers the user touched
```

A tier entry is a **plain model-id string** — no settings.

**Resolution (merge locus A — the server merges):** `pkg/config` exposes the
overrides only (it must not import the catalog — layering inversion). The
**server** resolves each `(runtime, tier)` as:

```
overrides[runtime][tier]  if present
else  catalogDefault(runtime, tier)   // server imports the catalog + RAM detect
```

So config stays catalog-free, untouched tiers always track catalog improvements
(e.g. Phase 1's GLM fix flows through automatically), and a customized tier is
taken wholesale. Customizations are **per-runtime and never ported** across
runtimes (a model id / setting valid for one runtime may be invalid for
another).

The `ModelTier{Cloud, Open}` struct is **deleted**. Cloud and open no longer
share a type or a tier enum, so a fourth cloud tier can never structurally
reappear.

**Explicitly dropped (were considered, rejected):**
- Per-model and per-tier *settings*. Recon showed launch settings
  (`ContextSize`, `ISQ`, `MaxSeqLen`, `GPULayers`, `ExtraArgs`, …) are run-host
  properties that already live per-runtime in `LlamaServerConfig`/
  `MistralRSConfig`; a model carries none. Tiers stay model-id-only.
- Hoarding a **full** tier set per runtime in config (interpretation 1). Config
  holds only overrides; the catalog is the default.

## Non-goals / decisions

- **No migration.** Clean break. Existing configs' flat `open:` string values and
  any `tiers.*.cloud` values are discarded on next load; the catalog default
  fills in. The user explicitly approved blowing away current values.
- **Overrides, not full sets.** Config persists only tiers the user changed
  (lazy, per-runtime). The catalog is the default; untouched tiers are never
  copied and always track the catalog.
- **Model-id-only tiers.** No per-model or per-tier settings. Run-host settings
  stay in `LlamaServerConfig`/`MistralRSConfig`, untouched.
- **Customizations are per-runtime and never ported.** Switching runtime never
  carries a model id or setting into a runtime where it may be invalid.
- **Cloud is out of scope except for deleting the retired residue.** The live
  vendor-keyed cost-tier cloud path is correct and untouched.
- **Merge locus A:** the server merges override-else-catalog-default (it can
  import the catalog and detect RAM); `pkg/config` stays catalog-free.
- The per-runtime curated catalogs (`mistralrs/catalog.json`,
  `llamaserver/catalog.json`) remain the source of runtime-valid default model
  ids and capability facts; Phase 1's `plain_chat_ok` / `status` fields stay.

## Blast radius (post-Phase-2 code — verified)

Phase 2 already deleted the cloud residue and collapsed the resolver to
`ResolveOpen(t) (string, bool)`. The current open shape is a flat
`ModelTiers{ MostCapable, Everyday, … ModelTier{Open string} }`. Phase 3
replaces that flat shape with runtime-keyed overrides.

`pkg/config` (data model + resolver → overrides-only):
- `models.go`: delete `ModelTier`/`ModelTiers`; add `OpenModels{ Overrides
  map[runtime]map[tier]string }` (or equivalent). Rewrite `ResolveOpen`,
  `OpenChatModel`, `OpenEmbeddingModel`, `TierSlots`, `ApplyModelTierPatch`,
  `tier()`/`tierSlot()` around `(runtime, tier) → override string`. Note:
  `ResolveOpen` returning `!ok` for a missing override is now *expected* — the
  server supplies the catalog default; config no longer defaults tiers itself.
- `config.go`: the `finalizeModelTiers` defaulting (~761–781, currently
  hardcodes `everyday=qwen3-coder`, `embedding=nomic-embed-text`) is **removed**
  — defaulting moves to the server/catalog. Env overrides (~956–968:
  `CERCANO_OPEN_MODEL`/`CERCANO_EMBEDDING_MODEL`) now write the *active*
  runtime's override. Delete the legacy `open_model→everyday` migration.
- `models_test.go`, `config_test.go`, `tierrecs_test.go`: update for the new
  shape.

Server (merge locus A — new):
- `internal/server`: the resolver that today calls `ResolveOpen` must become
  `override else catalogDefault(runtime, tier)`, importing the catalog + RAM
  detection. This is the merge point. `rebindOpenTiersForRuntime` /
  runtime-switch reloads the active runtime's override-over-default set.
- `config_watcher.go`: open-override change detection for the new shape.

Worker wire (Phase 4) & CLI (Phase 6) handled in their own phases.

Must NOT be touched (live cloud path, unrelated to this change):
- `inference.Tiers.Cloud` (the built cloud *TurnRunner*), `ResolveCloudModelForTier`,
  `ModelProfiles.Cloud.Providers`, `CloudProfiles`/`ActiveCloudProfile`.

## Acceptance

- `ModelTier`/`ModelTiers` flat shape is gone; open config is runtime-keyed
  overrides (`overrides[runtime][tier] = model-id`), model-id strings only.
- Config persists **only** tiers the user changed, lazily, per runtime; untouched
  tiers resolve from the catalog default. No full per-runtime sets in config.
- The server merges `override else catalogDefault(runtime, tier)`; `pkg/config`
  does not import the catalog.
- Switching `open_runtime` resolves that runtime's override-over-default set —
  no stale cross-runtime values, no reconciliation, no cross-runtime porting.
- Cloud resolution flows only through the vendor-keyed cost-tier path; the retired
  `.cloud` capability slots and the `cloud:` recommendations block are deleted.
- Open tier defaults are picked from the per-runtime catalog by detected RAM;
  GLM stays present but flagged `plain_chat_ok:false` and is never auto-selected
  as a plain-chat default (Phase 1 retained).
- No migration code remains for the legacy flat `open:` string or `tiers.*.cloud`.
- `go build ./...` and `go test ./...` pass for both server and CLI.
