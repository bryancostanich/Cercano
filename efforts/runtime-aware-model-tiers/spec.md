# Runtime-aware, RAM-aware, profile-rich model tiers

## Problem

`config.ModelTier.Open` is a single string, but Cercano supports three open
runtimes (`ollama`, `mistralrs`, `llama_server`) whose model identifiers are
mutually incompatible (Ollama tags vs mistral catalog IDs vs llama-server GGUF
paths). Switching `open_runtime` does **not** switch the model set — it leaves a
now-wrong `Open` string, producing split-brain state (config says one runtime,
tiers route to another runtime's model).

There is also no capability metadata per model, so the system cannot express
facts we verified empirically this session:

- qwen UQFF on mistral.rs: loads, but multi-turn tool use is broken.
- GLM GGUF on mistral.rs: does not load (`glm4moe` rejected).
- GLM GGUF on llama-server: loads, tool-call/tool-result work, but **plain chat
  returns empty content** (`plain_chat_ok = false`).
- qwen GGUF on llama-server: fully works (tools + plain chat).

## Goal

1. Make the open side of a tier **runtime-aware**: each tier carries a model
   per open runtime, so switching `open_runtime` is lossless and needs no
   reconciliation.
2. Introduce a **rich model profile** concept: a catalog of known, tested
   `(runtime, artifact, capability)` records (`supports_tools`, `plain_chat_ok`,
   `status`, `min_ram_gb`).
3. Provide a **recommended matrix**: `(runtime × RAM level) → recommended model
   per tier`, so setup and runtime-switch pick correct defaults for the machine.

## Non-goals / decisions

- **No migration.** Clean break. Existing configs' flat `open:` string values
  are discarded and replaced by the stock matrix defaults on next load. The user
  explicitly approved blowing away current `open:` tier values.
- RAM buckets reuse the exact thresholds already in
  `pkg/config/mistralrs_memory.go` (`<32`, `32–63`, `64–127`, `128+`), factored
  into one shared function so they never diverge.
- The recommended matrix lives in the existing embedded
  `pkg/config/tier_recommendations.yaml` (extended), not a new file (option D).
- The profile catalog is data (embedded YAML), separate from user config.

## Reality check (discovered during Phase 1 recon — supersedes parts of the plan below)

Much of the "matrix" already exists. Do NOT rebuild it:

- Per-runtime curated catalogs already ship: `mistralrs/catalog.json` and
  `llamaserver/catalog.json`, each with RAM-tiered `profiles`
  (`"24"/"48"/"96"/"128"` GB → tier → model ID) and a `CuratedModel` dictionary.
- `CuratedCatalog.ProfileForRAM(bytes)` already does the RAM-bucket lookup.
- `CuratedModel` already carries `SupportsTools` and `SupportsEmbed`.
- `server.recommendedOpenModels(ram)` already switches on the active runtime.
- The setup wizard already prefers this per-runtime RAM-tiered catalog over the
  flat `tier_recommendations.yaml` open block.

So the matrix (D) is largely done. The real remaining gaps are:

1. **`ModelTier.Open` is still a single string** → the resolved/persisted tier is
   not runtime-keyed, so switching `open_runtime` leaves a stale cross-runtime
   value (the split-brain). This is the core defect and the main work (B).
2. **No capability field for "loads + tools work but plain chat broken."**
   `CuratedModel` has `SupportsTools`/`SupportsEmbed` but not `PlainChatOK`/
   `Status`. The llama `128` profile currently recommends `glm-4.5-air` for
   `most_capable`, which we verified returns empty content on plain chat — a live
   bad default that must be fixed and made expressible.
3. **Two overlapping open-recommendation systems** (catalog.json profiles vs the
   `tier_recommendations.yaml` `open:` block). The flat YAML open block is now
   redundant/misleading and should be retired or reduced to a non-open fallback.

## Approved design (B + D)

Schema:

```go
type ModelProfile struct {
    ID            string // "qwen3-30b-instruct-mistralrs", "glm-4.5-air-llama"
    Runtime       string // mistralrs | llama_server | ollama
    Model         string // wire model / catalog id / gguf path for that runtime
    MinRAMGB      int
    SupportsTools bool
    PlainChatOK   bool
    Status        string // tested | experimental | broken
}

type ModelTier struct {
    Cloud string            `yaml:"cloud"`
    Open  map[string]string `yaml:"open"` // runtime -> profileID (or model id)
}
```

Resolution: `id := tier.Open[cfg.OpenRuntime]` → profile catalog lookup →
runtime/model/capabilities. Switching runtime is lossless.

Matrix data (`tier_recommendations.yaml`, replacing the flat `open:` block):

```yaml
open:
  <runtime>:
    "<ram_bucket_gb>":
      most_capable: [profileID, ...]
      everyday: [...]
      fast_light: [...]
      fast_light_text: [...]
profiles:
  <profileID>: { runtime, model, min_ram_gb, supports_tools, plain_chat_ok, status }
```

## Acceptance

- `ModelTier.Open` is a per-runtime map everywhere it is read/written
  (config, worker wire protocol, server switch logic, resolver, UI).
- Switching `open_runtime` selects that runtime's tier models with no stale
  cross-runtime values.
- The profile catalog encodes the verified capability facts above; GLM is
  present but flagged `plain_chat_ok: false` and is never auto-selected as a
  plain-chat default.
- Setup / runtime-switch pick tier defaults from the matrix by detected RAM.
- `go build ./...` and full `go test ./...` pass for both server and CLI.
- No migration code remains for the legacy flat `open:` string.

## Blast radius (known read/write sites)

- `pkg/config/models.go`: `ModelTier`, `side()`, `Resolve()`, `OpenChatModel()`,
  `ApplyModelTierPatch()`, `TierSlots()`, `tier()/tierSlot()`.
- `pkg/config/config.go`: default/finalize logic (`finalizeModelTiers`,
  `Everyday.Open`/`Embedding.Open` defaulting), delete legacy migration.
- `pkg/config/tierrecs.go` + `tier_recommendations.yaml`: matrix + profiles.
- `internal/worker/wire.go`: `TierEverydayOpen` etc. flat wire fields.
- `internal/server/server.go`: `rebindOpenTiersForRuntime`, switch flow.
- `internal/server/config_watcher.go`: `Everyday.Open` change detection.
- CLI `internal/ui`: tier/runtime pickers.
- ~15 test files referencing `.Open` as a string.
