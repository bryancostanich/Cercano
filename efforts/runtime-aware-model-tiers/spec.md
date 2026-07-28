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

2. **Make the open side runtime-keyed (runtime-outer).** A config carries one
   **full open tier set per runtime**, with an active-runtime pointer. Switching
   `open_runtime` swaps in that runtime's whole set losslessly — no runtime-blind
   string, no reconciliation. This mirrors the per-runtime `catalog.json` shape
   the codebase already uses.

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

**Open — runtime-outer, a full tier set per runtime:**

```yaml
models:
  open_runtime: llama_server        # the active runtime (already exists as cfg.OpenRuntime)
  open:
    runtimes:
      llama_server:
        most_capable:    qwen3-30b-thinking
        everyday:        qwen3-coder
        fast_light:      ...
        fast_light_text: ...
        embedding:       nomic-embed-text
      mistralrs:
        most_capable:    ...
        everyday:        ...
        fast_light:      ...
        fast_light_text: ...
        embedding:       ...
```

Resolution for the open side: `open.runtimes[cfg.OpenRuntime][tier]`. Switching
`cfg.OpenRuntime` selects a different, complete, internally-consistent set.

The `ModelTier{Cloud, Open}` struct is **deleted**. Cloud and open no longer
share a type or a tier enum, so a fourth cloud tier can never structurally
reappear.

## Non-goals / decisions

- **No migration.** Clean break. Existing configs' flat `open:` string values and
  any `tiers.*.cloud` values are discarded on next load and replaced by the
  runtime's stock defaults. The user explicitly approved blowing away current
  values.
- **Runtime-outer, not tier-outer.** The runtime owns its full tier set; the whole
  set swaps on runtime change. (Explicitly chosen over "tier owns a per-runtime
  map".)
- **Cloud is out of scope except for deleting the retired residue.** The live
  vendor-keyed cost-tier cloud path is correct and untouched.
- RAM buckets reuse the existing thresholds (`<32`, `32–63`, `64–127`, `128+`)
  already used by the per-runtime catalogs; open defaults are picked from the
  per-runtime `catalog.json` by detected RAM (this data already exists — do not
  rebuild it).
- The per-runtime curated catalogs (`mistralrs/catalog.json`,
  `llamaserver/catalog.json`) remain the source of runtime-valid model ids and
  capability facts; Phase 1's `plain_chat_ok` / `status` fields stay.

## Blast radius (verified read/write sites)

Open side (`ModelTier.Open` string → runtime-keyed set):
- `pkg/config/models.go`: `ModelTier`, `side()`, `Resolve()`, `OpenChatModel()`,
  `OpenEmbeddingModel()`, `ApplyModelTierPatch()`, `TierSlots()`,
  `tier()`/`tierSlot()`.
- `pkg/config/config.go`: open defaulting/finalize (lines ~761–781), env
  overrides (~956–968), delete legacy flat-`open` migration.
- `pkg/config/tierrecs.go` + `tier_recommendations.yaml`: open block; **delete**
  the `cloud:` block.
- `internal/worker/wire.go`: `TierEverydayOpen` etc. flat wire fields.
- `internal/server/server.go`: `rebindOpenTiersForRuntime`, runtime-switch flow.
- `internal/server/config_watcher.go`: `Everyday.Open` change detection.
- CLI `internal/ui`: tier/runtime pickers.
- Tests referencing `.Open` as a string:
  `models_test.go`, `models_migrate_test.go` (delete), `config_test.go`,
  `tierrecs_test.go`, plus server/worker tests.

Cloud residue to delete (do NOT touch the live vendor-keyed path):
- `ModelTier.Cloud` field + `.cloud` YAML tag (`models.go:41`).
- `side()` cloud branch (`models.go:99`), `ApplyModelTierPatch` cloud write
  (`models.go:156`), `TierSlots` cloud emit (`models.go:176`).
- The `cloud:` block in `tier_recommendations.yaml` and its loader/consumer in
  `tierrecs.go` (`r.Cloud`, lines ~59/62/104) — confirm it is only wizard
  autofill, then remove.
- `models_test.go:142` (`Everyday.Cloud = ...`).

Must NOT be touched (different meaning of "Cloud" — live cloud runtime):
- `inference.Tiers.Cloud` (the built cloud *TurnRunner*), `router.go` `Tiers`,
  `main.go:319/584`, `ResolveCloudModelForTier`, `ModelProfiles.Cloud.Providers`,
  `CloudProfiles`/`ActiveCloudProfile`, all `cmd/*/main.go` cloud-provider wiring.

## Acceptance

- `ModelTier` is gone. Cloud and open are separate types with separate tier
  vocabularies; there is no struct where a fourth cloud tier can exist.
- The open side is a per-runtime full tier set (runtime-outer). Reads resolve via
  `open.runtimes[cfg.OpenRuntime][tier]`.
- Switching `open_runtime` selects that runtime's tier set with no stale
  cross-runtime values and no reconciliation.
- Cloud resolution flows only through the vendor-keyed cost-tier path; the retired
  `.cloud` capability slots and the `cloud:` recommendations block are deleted.
- Open tier defaults are picked from the per-runtime catalog by detected RAM;
  GLM stays present but flagged `plain_chat_ok:false` and is never auto-selected
  as a plain-chat default (Phase 1 retained).
- No migration code remains for the legacy flat `open:` string or `tiers.*.cloud`.
- `go build ./...` and `go test ./...` pass for both server and CLI.
