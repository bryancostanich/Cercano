# Plan — Unified Model Catalog + DeepInfra Source

Ordering principle: generalize the interface first with **zero behavior
change**, prove it against the two existing sources, then add DeepInfra as the
first source that exercises the new axis. This keeps the risky refactor and
the new integration in separate, separately-revertable commits.

---

## Step 1 — Split `Backend` into `Source` + `Downloadable`

**Files:** `source/server/internal/catalog/catalog.go`

- [x] Rename `Backend` → `Source`; drop `ResolveDownload` from the core interface
- [x] Add `Downloadable` (embeds `Source`, adds `ResolveDownload`)
- [x] Keep `Model`/`Detail`/`File`/`DownloadPlan` byte-identical for now
- [x] Retain `type Backend = Source` deprecated alias; removed in Step 6
  **Verify:** `go build ./...` clean. No behavior change.
                
                ---

## Step 2 — Assert the capability at the download call sites

**Files:** `source/server/internal/server/server.go` (~2281, ~2370),
`source/server/internal/server/model_ram_estimate.go`

- [x] `buildCatalogDownloadRecord` takes `catalog.Downloadable`, not `Source`
- [x] At `DownloadRuntimeModel`, type-assert; on failure return a clear error
- [x] Add `var _ catalog.Downloadable = (*Backend)(nil)` to both backends
  **Verify:** existing catalog/server tests pass unchanged.
                
                ---

## Step 3 — Generalize `Model` and `Detail`

**Files:** `catalog.go`, `modelcatalog/hf_backend.go`,
`ollamacatalog/ollama_backend.go`

- [ ] Add `Publisher`, `SupportsVision`, `Kind`, `Deprecated`, `ReplacedBy` to
  `Model`; map HF `Author` → `Publisher`
- [ ] Introduce `Variant` (`Name`, `Quantization`, `SizeBytes`, `PriceIn`,
  `PriceOut`); `Detail.Files []File` → `Detail.Variants []Variant`
- [ ] Update `pickDefaultQuant` and `filesForDownload` to operate on `Variant`;
  Q4_K_M preference and shard-grouping logic unchanged
                
                **Verify:** existing HF and Ollama backend tests pass with only
                type/field renames. Any test whose *expectations* change is a red flag —
                this step must not alter behavior.
                
                ---

## Step 4 — Multi-source registry

**Files:** `catalog.go`, `server.go`, `cmd/cercano/main.go` (~579-585)

- [ ] Add `Registry.All() []Source` and `Registry.Get(name) (Source, bool)`
- [ ] Keep `Active()`/`SetActive()` for downloadable-source selection
- [ ] `ListRuntimeModels` fans out across `All()`, preserving current dedupe
- [ ] Per-source list failure omits that source rather than failing the call
  **Verify:** with only HF + Ollama registered, list output is equivalent to
                today's. Add a test with two stub sources asserting merge + partial-failure
                tolerance.
                
                ---

## Step 5 — DeepInfra source

**New:** `source/server/internal/deepinfracatalog/`

- [ ] Client against `https://api.deepinfra.com/models/list` (no auth)
- [ ] Implements `Source` and `Priced`; **does not** implement `Downloadable`
- [ ] Eligibility filter applied in `List`
- [ ] Map `quantization` → `Variant.Quantization`; normalize pricing
- [ ] In-process TTL cache, serve stale on fetch failure
  Eligibility filter detail:
                  - `type == "text-generation"` only — drops text-to-image, text-to-speech,
                    text-to-video, embeddings, speech-to-text.
                  - must carry the `"tools"` tag.
                  - drop entries with a non-zero `deprecated` timestamp; retain
                    `replaced_by` on `Detail` so a pinned-but-retired model can be explained.
                - Map `quantization` → `Variant.Quantization`; pricing (cents-per-token) →
                  normalized per-million-token `PriceIn`/`PriceOut`.
                - Cache the index in-process with a TTL and serve stale on fetch failure.
                  Mirrors the existing online-catalog posture: browse is on-demand, an error
                  degrades rather than fails.
                
                **Verify:** unit tests against a recorded `/models/list` fixture — filter
                drops non-text and tool-less and deprecated entries; pricing math is exact;
                `var _ catalog.Downloadable = (*Source)(nil)` **fails to compile** (assert via
                a negative test that the runtime type assertion returns false).
                
                ---

## Step 6 — Wire in, and give DeepInfra a cost-tier table

**Files:** `cmd/cercano/main.go`, `pkg/config/config.go` (~590),
`internal/cloudcatalog/cloudcatalog.go` (~53)

- [ ] Register the DeepInfra source in the registry
- [ ] Add a `deepinfra` entry to `bakedCloudCatalog()` mapping cost tiers to
  concrete model ids so `ResolveCloudModelForTier` stops returning `""`
- [ ] Revisit `Tier: TierUntested` on the DeepInfra cloud profile
- [ ] Remove the `Backend = Source` alias from Step 1
  **Verify:** `ResolveCloudModelForTier("deepinfra", <each tier>)` returns
                non-empty. `cloudcatalog` remains I/O-free — the HTTP index lives in
                `deepinfracatalog`, not there.
                
                ---

## Step 7 — Wire shape for the picker

**Files:** `source/proto/agent.proto` (`RuntimeModel`, ~548), server mappers

`catalogModelToProto` currently hardcodes `Id: "llama_server:online:"+m.ID`,
`Runtime: "llama_server"`, `Format: "gguf"` — untrue for a DeepInfra model.

- [ ] Add `source`, `publisher`, `kind`, `deprecated`, `replaced_by`,
  `price_in`/`price_out`, and `acquisition` (`download` | `serve`) fields
- [ ] Populate `runtime`/`format` only for downloadable sources
- [ ] Keep existing field numbers; append new ones, no renumbering
  **Verify:** CLI models page renders both local and DeepInfra entries; a
                DeepInfra entry offers no download affordance.
                
                ---

## Risks

| Risk | Mitigation |
|---|---|
| Step 3 silently changes download behavior | Steps 1–4 must not alter any existing test's expectations; only renames |
| `/models/list` is unversioned and may change shape | Parse defensively, tolerate unknown fields, cache-and-serve-stale; fixture test pins current shape |
| Registry fan-out slows the models page | Per-source TTL cache; fan out concurrently; per-source failure is non-fatal |
| Proto churn breaks an older CLI | Append-only fields, no renumbering |

## Commit boundaries

One commit per step. Steps 1–4 are a pure refactor and revertable as a unit
without touching DeepInfra; Steps 5–7 add the integration.
