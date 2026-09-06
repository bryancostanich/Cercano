# Unified Model Catalog + DeepInfra Source

## Problem

Cercano discovers models through `catalog.Backend`, an interface built on the
assumption that a model is a **file you download**. Both implementations
(HuggingFace, Ollama) fit that shape. DeepInfra does not: its models are
*servable*, never downloaded.

Adding DeepInfra by conforming to `Backend` would force a `ResolveDownload`
that exists only to return an error. Adding it as a separate parallel stack
would fork model discovery in two, permanently.

Neither is acceptable. The divergence between a HuggingFace repo and a
DeepInfra model is narrower than the current interface implies — it is
**acquisition only**. Everything else (identity, publisher, context length,
tool support, quantization, variants, search, lifecycle) is shared.

Secondarily: DeepInfra is defined as a cloud profile
(`cloudcatalog.go:53`, `Tier: TierUntested`) but has **no cost-tier table**.
`bakedCloudCatalog()` (`config.go:590`) seeds only `anthropic`, `openai`, and
`openai-chatgpt`, so `ResolveCloudModelForTier` returns `""` for DeepInfra and
every tiered request silently falls back to the profile's default `Model`.

## Audit findings (verified in code)

These are established, not assumed:

- **Failover already preserves tier in both directions.** `resilience.Options`
  carries both `PrimaryModelFor(tier)` and `BackupModelFor(tier)`;
  `primaryRequest` normalizes before the first attempt, `backupRequest`
  rewrites into the backup vendor's namespace. Anthropic-Premium →
  OpenAI-Premium works today. **No change needed.**
- **The open-lane merge has exactly one home.** `openmodels.Resolver.Model(t)`
  does override-else-catalog-default keyed on `c.OpenRuntime`. The package doc
  states the constraint: `pkg/config` must not import
  `internal/localruntime`, so the merge stays server-side.
- **The cloud lane has no override layer.** `bakedCloudCatalog()` seeds vendor
  tables into config at load; `stripBakedCloudCatalogForSave` deletes them at
  save. Cloud has seed *semantics* implemented by erasure rather than by
  diffing — which is why a user edit to a cloud tier slot does not survive a
  write. Open solves this with `Overrides[runtime][tier]`; cloud has no
  equivalent.
- **`cloudcatalog` is declared pure.** Its package doc: *"pure and
  deterministic — no I/O, no config dependency — so it is trivially
  unit-testable and safe to call under a lock."* A live HTTP index cannot live
  there.
- **`catalog.Registry` allows exactly one active backend.** `SetActive` /
  `Active()` are single-valued, driven by `cfg.Catalog.Backend`.
- **DeepInfra's index needs no auth.** Both `/v1/openai/models` and
  `/models/list` respond unauthenticated, so model browse works before the
  user has supplied an API key.

## Confirmed decisions

1. **Index endpoint: `/models/list`** (DeepInfra-native), not
   `/v1/openai/models`. Decisive factor: it is the only one exposing a
   `"tools"` tag. Cercano's agentic loop is useless on a model that cannot
   call tools. It also carries `deprecated` (timestamp) and `replaced_by`,
   which is exactly the data needed to tell a user their pinned model was
   retired and name its successor.
   Accepted cost: a non-standard endpoint with no compatibility guarantee.

2. **Unification via a core `Source` interface plus optional capability
   interfaces** (the `http.Flusher` / `http.Hijacker` pattern). All sources
   implement `List`/`Detail`; only downloadable sources implement
   `Downloadable`. Chosen over a runtime-switched `AcquisitionPlan` sum type
   and over a fat interface with cloud stubs, because it is the only option
   where the **type system** carries the distinction:
   `buildCatalogDownloadRecord` takes a `Downloadable`, making it impossible
   to route a DeepInfra model into the download manager.

3. **This lands early**, as part of the DeepInfra body of work — not deferred
   behind it.

## Target shape

```go
// Source is any place models come from — downloadable or servable.
type Source interface {
    Name() string
    List(ctx context.Context, opts ListOptions) ([]Model, error)
    Detail(ctx context.Context, id string) (Detail, error)
}

// Downloadable is implemented only by sources whose models become local
// files. Consumers type-assert; a servable-only source cannot be handed to
// the download manager.
type Downloadable interface {
    Source
    ResolveDownload(ctx context.Context, id, file string) (DownloadPlan, error)
}

// Priced is implemented by sources that meter usage.
type Priced interface {
    Source
    Pricing(ctx context.Context, id string) (Pricing, error)
}

type Model struct {
    Source        string
    ID            string
    Publisher     string   // HF author, DeepInfra org prefix
    ContextLength int
    SupportsTools bool
    SupportsVision bool
    Kind          Kind     // text-generation, embedding, image, speech, …
    Deprecated    bool
    ReplacedBy    string   // successor id when Deprecated
    Downloads     int      // popularity signal; source-dependent
    Likes         int
}

type Variant struct {
    Name         string
    Quantization string
    SizeBytes    int64  // downloadable sources
    PriceIn      int64  // priced sources; per-million-token, normalized
    PriceOut     int64
}
```

`SizeBytes` populates for local, prices for cloud, neither mandatory.
`Detail.Files` generalizes to `Detail.Variants` — a quant file and a
turbo/quantized cloud variant are the same concept with different payloads.

## Two concepts that generalize (previously mislabeled as cloud-vs-local)

- **The gate.** Local gates architecture against the runtime
  (`runtimeArchSupported`). Cloud gates tags against the role — no `"tools"`
  tag means no agentic loop. Both answer *"can this model serve this role
  here?"* and become one consumer-side eligibility check.
- **Variant selection.** Local picks a quant (`pickDefaultQuant` prefers
  Q4_K_M). Cloud picks among turbo/quantization variants of the same base.

## Open design point (needs a call during execution)

**Single-active vs. multi-source registry.** `catalog.Registry` is
single-active today. A unified picker showing both local and cloud models
requires either:

- **Multi-source registry** — `Registry.All()` alongside `Active()`; browse
  fans out across sources, local keeps an "active downloadable" for the
  download path. Truer to the goal; touches every `reg.Active()` call site.
- **Keep single-active, add source scoping to the request** — the client asks
  for a named source. Smaller change; the picker does the fan-out client-side.

Recommendation: **multi-source registry**, because the fan-out belongs
server-side where the eligibility gate already lives.

## Out of scope

- The Primary/Secondary/Local tier restructure (separate, later effort).
- Replacing `IsCloud` (126 non-test uses, 27 files, 4 proto fields).
- Cloud tier-override persistence — noted above as a real gap, but it is a
  config-layer fix independent of discovery.

## Success criteria

- A DeepInfra source lists tool-capable text-generation models, filtered of
  image/speech/video/embedding entries and of deprecated models.
- DeepInfra gets a real cost-tier table so `ResolveCloudModelForTier` stops
  returning `""`.
- HuggingFace and Ollama behave identically to today; download paths unchanged
  in observable behavior.
- A servable-only source cannot be passed to the download manager — enforced
  at compile time, with a test asserting the DeepInfra source does not satisfy
  `Downloadable`.
