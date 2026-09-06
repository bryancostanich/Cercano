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

- [x] Add Publisher/SupportsVision/Kind/Deprecated/ReplacedBy to Model
- [x] Introduce Variant and change Detail.Files to Detail.Variants
- [x] Update pickDefaultQuant and filesForDownload to operate on Variant
  **Verify:** existing HF and Ollama backend tests pass with only
                        type/field renames. Any test whose *expectations* change is a red flag —
                        this step must not alter behavior.
                        
                        ---

## Step 4 — Source-qualified refs; registry becomes a lookup table

Superseded the original "keep `Active()` for downloads, add `All()` for
browse" shape. The audit found `Active()` had three consumers wanting two
different things: browse wants every source, while download and RAM-estimate
want *the source this model came from* and were using the active source as a
proxy for it. That proxy is only correct while one source is enabled, and it
is a live bug today — changing `catalog.backend` after a download makes both
paths resolve the id against the wrong source. Keeping `Active()` would have
fixed one consumer of three and left the confusion in place, so the reference
model was fixed instead.

**Files:** `catalog.go`, `server.go`, `model_ram_estimate.go`,
`cmd/cercano/main.go`, `pkg/config/config.go`, `pkg/agentclient/client.go`,
`proto/agent.proto`, CLI `runtime_estimate.go` / `runtime_dashboard.go` /
`wizard_page.go`

- [x] Add `catalog.Ref{Source, ID}`; a catalog id never travels alone
- [x] Drop `Active`/`SetActive`/`ActiveName`; add `Lookup`, `LookupDownloadable`, `All`
- [x] Add `Resolve` / `ResolveDownloadable`; the latter refuses a servable-only source
- [x] Unqualified ref (older client) resolves by probing sources, not by assuming one
- [x] `ListRuntimeModels` fans out across `All()`, preserving dedupe; a failing source is omitted
- [x] Carry `catalog_source` beside `catalog_id` in the three proto messages
- [x] `agentclient.CatalogRef` + `CatalogRefOf`; CLI echoes the pair back
- [x] Scope the CLI estimate cache key by source
- [x] Delete the `catalog.backend` config key — nothing left to select between
  **Verify:** server + CLI build, vet, and full suites green. Existing CLI
                        estimate tests pass *unchanged*, confirming the unqualified path preserves
                        today's behavior. Mutation-checked the new `Resolve` test: forcing the
                        qualified branch off makes `ollama/shared-id` resolve to `huggingface`
                        and fails, so the test genuinely catches the bug it describes.
  **Commits:** `d66a7756`, `f45da9ab`
                        
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

- [x] `catalog_source` (field 24) — landed early in Step 4, since download and
  RAM-estimate need the qualified ref regardless of how the picker renders
- [ ] Add `publisher`, `kind`, `deprecated`, `replaced_by`,
  `price_in`/`price_out`, and `acquisition` (`download` | `serve`) fields
- [ ] Populate `runtime`/`format` only for downloadable sources
- [x] Keep existing field numbers; append new ones, no renumbering
  **Verify:** CLI models page renders both local and DeepInfra entries; a
                        DeepInfra entry offers no download affordance.
                        
                        ---

## Risks

| Risk | Mitigation |
|---|---|
| Step 3 silently changes download behavior | Steps 1–4 must not alter any existing test's expectations; only renames |
| `/models/list` is unversioned and may change shape | Parse defensively, tolerate unknown fields, cache-and-serve-stale; fixture test pins current shape |
| Registry fan-out slows the models page | Per-source failure is non-fatal (done in Step 4). Fan-out is currently **sequential** — acceptable for two fast sources, but must become concurrent, with a per-source TTL cache, before DeepInfra's network index joins the loop in Step 5 |
| Unqualified-ref probe costs one `Detail` call per source | Only reached for a pre-Step-4 client; current clients always send the source. Probe stops at the first hit and skips failing sources |
| Proto churn breaks an older CLI | Append-only fields, no renumbering |

## Commit boundaries

One commit per step. Steps 1–4 are a refactor revertable as a unit without
touching DeepInfra; Steps 5–7 add the integration.

Steps 1–3 were pure renames with no behavior change. Step 4 is the exception:
it deliberately *changes* behavior, because the behavior was wrong — a catalog
id used to resolve against whichever source config named, rather than the one
that issued it. That is why Step 4's verification includes a mutation check
rather than a "tests unchanged" assertion.
