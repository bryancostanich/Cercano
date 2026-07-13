# mistral.rs integration — sketch

> **Status (2026-07-12): proposal / file-by-file sketch, no code yet.** This
> maps how a second local runtime — **mistral.rs** — plugs into the seams that
> shipped with the pluggable model catalog (see
> [`../cli/model-catalog-online/design.md`](../cli/model-catalog-online/design.md))
> and the runtime bake-off (see [`runtime-evaluation.md`](runtime-evaluation.md)).
> It exists so the concrete work is scoped before anyone writes it. Every seam
> named below was read from `main`; the mistral.rs-side files are new and
> modelled on the llama-server equivalents.
>
> **Decisions D1–D3 resolved 2026-07-12** — see the Decisions section at the
> end. Where a Piece's inline recommendation differs from a resolved decision,
> the resolved decision governs.

## Why, in one paragraph

llama-server (GGUF) can't load the newest hybrid-MoE architectures —
`qwen3next` is the triggering example, and `llamacompat` deliberately gates it
out. **mistral.rs can load them today** (via safetensors + in-situ quant, or a
pre-quantized UQFF), on Apple Metal and CUDA. So mistral.rs is not a
replacement for llama-server — it's a **second runtime beside it**, selected per
platform/model, that extends the set of architectures Cercano can run locally.
The runtime-evaluation doc's recommendation stands: add it beside llama-server,
keep llama-server the near-term default, gate next-gen-MoE-on-Metal as opt-in
until the upstream Metal fixes land.

## The two axes it plugs into

Cercano already separates two orthogonal things, and mistral.rs touches both:

| Axis | Interface | Today | mistral.rs adds |
|---|---|---|---|
| **Discovery source** — where model listings come from | `catalog.Backend` (`internal/catalog`) | HuggingFace (GGUF), Ollama | safetensors/UQFF discovery on HF |
| **Runtime** — what serves the weights | `localruntime.Provider` (`internal/localruntime`) | llama-server (GGUF) | mistral.rs (safetensors/UQFF) |

The **compatibility gate is the coupling**: a model from a source is only
offered if the *target runtime* can load it. That coupling is where most of the
non-mechanical work lives, because today it's hardwired to one runtime.

The bulk of the integration is a new **Provider**; the genuinely new (non-copy)
pieces are the **safetensors/UQFF download** and making the **gate
runtime-aware**.

---

## Piece 1 — the runtime provider (bulk; mostly mirrors llama-server)

New package `internal/localruntime/mistralrs/`, implementing
`localruntime.Provider`:

```go
Name() string
Capabilities() localruntime.RuntimeCapabilities
Discover(ctx) ([]localruntime.ModelRecord, error)
Start(ctx, localruntime.StartRequest, localruntime.LogSink) (*localruntime.InstanceRecord, error)
Stop(ctx, instanceID string) error
Probe(ctx, instanceID string) (*localruntime.InstanceHealth, error)
```

The supervisor machinery in `llamaserver/provider.go` — `resolveBinary`,
`choosePort`, `startProcess`, `waitReady`, `watch` (crash + restart-backoff),
`kill`, `finishReadiness`, `pipeLogs`, orphan sweep — **copies almost verbatim**;
mistral.rs is also a long-lived child process exposing an OpenAI-compatible HTTP
server, so process isolation, readiness polling, and log piping are identical.
Files to mirror: `provider.go`, `install.go` (binary discovery + prebuilt
install), `orphans.go` + `process_*.go` (pid registry + `SweepOrphans`), plus
`*_test.go`.

**What actually differs is `argsFor` and the launch shape.** llama-server takes
`--model file.gguf --ctx-size … --gpu-layers …`. mistral.rs is
subcommand-shaped and format-dependent:

- **safetensors + ISQ** (in-situ quant at load):
  `mistralrs-server --port <p> plain -m <repo-or-dir> --isq Q4K`
- **pre-quantized UQFF**:
  `mistralrs-server --port <p> uqff -m <dir> -f model.uqff`
- **GGUF** (mistral.rs can also read GGUF): `… gguf -m <dir> -f model.gguf`

So `argsFor` switches on `ModelRecord.Format` (`"safetensors"` | `"uqff"` |
`"gguf"`) and reads mistral.rs-specific knobs from config (ISQ level, device
layers). `Capabilities()` returns `ManagedProcesses:true, CanStart/Stop/Restart,
SupportsChat, SupportsTools` (mistral.rs speaks native tool-calls),
`SupportsEmbed` per model.

`Discover()` walks the model dir for mistral.rs-shaped models (a directory with
`config.json` + `*.safetensors`, or a `*.uqff`) and returns `ModelRecord`s with
`Runtime:"mistralrs"`, plus the provider's own embedded curated catalog
(Piece 8), exactly as llamaserver's `Discover` merges on-disk + `catalogModels()`.

## Piece 2 — the engine/LLM adapter (small; mirrors engine/llamaserver)

New package `internal/engine/mistralrs/`, mirroring the two files in
`internal/engine/llamaserver/`:

- `engine.go` — an `InferenceEngine` over mistral.rs's `/v1` (chat, stream). Because
  mistral.rs is **OpenAI-compatible**, this is nearly the llama-server engine
  with a different base URL and health path.
- `llmprovider.go` — the `llm.Provider` wrapper the agent tiers consume.

If the two engines end up near-identical, a follow-up can factor a shared
`openaiengine` and parameterize base URL / quirks — but start by copying, per
the "duplicate then de-duplicate once the shape is proven" rule the catalog
rework itself followed.

## Piece 3 — the compatibility gate (new; sibling to llamacompat)

New package `internal/mistralrscompat/`, the structural twin of
`internal/llamacompat`: `Supported(arch) bool`, `Normalize`, `SupportedArches()`,
seeded from **mistral.rs's loader registry** (its `normal_loaders.rs` +
`vision_loaders.rs` `NormalLoaderType`/arch set), with a test asserting the
curated mistral.rs catalog's arches are all in it.

The headline difference from `llamacompat`: **this set contains `qwen3next` and
the other hybrid-MoE arches** llama.cpp lacks. That divergence *is* the feature —
the two gates encode "what each runtime can actually load," and mistral.rs's
admits the models llama-server's rejects.

As with `llamacompat`, the honest long-term is to generate the set from the
pinned mistral.rs build rather than hand-maintain it; a hand seed is the MVP.

## Piece 4 — make the gate runtime-aware (server rewiring)

Today `server.go::buildCatalogDownloadRecord` hardcodes the gate:

```go
if !llamacompat.Supported(detail.Architecture) { … refuse … }
```

That's correct only because llama-server is the sole runtime. With two runtimes
the gate must be **selected by the target runtime** (the `runtime` argument
already flows into `buildCatalogDownloadRecord`):

```go
if !gateFor(runtime).Supported(detail.Architecture) { … refuse … }
// gateFor("llama_server") -> llamacompat ; gateFor("mistralrs") -> mistralrscompat
```

Small, well-contained, and testable the same way the existing refusal test is
(`TestBuildCatalogDownloadRecord_GatesUnsupportedArch`) — add a mistral.rs case
asserting `qwen3next` is *admitted* for `mistralrs` and *refused* for
`llama_server`. This is the single most important correctness change: it stops
the gate from wrongly rejecting a model the active runtime can run.

## Piece 5 — online discovery of safetensors/UQFF (a real design choice)

The HF backend today queries `filter=gguf` and reads arch from
`gguf.architecture`. safetensors/UQFF models need `filter=safetensors` and arch
from the repo's `config.json` (`architectures[0]`, e.g. `Qwen3NextForCausalLM`
→ normalize → `qwen3next`). Two ways to fit the single-active-backend model:

- **Option A — parameterize the HF backend by format (recommended).** Add a
  `Format` to `catalog.ListOptions` and `catalog.Detail`; the HF backend
  switches filter + arch-source on it. The **active runtime** implies the
  format (llama-server→gguf, mistralrs→safetensors/uqff), so the user never has
  to think about it. Cost: a one-field interface change to `catalog.Backend`
  inputs.
- **Option B — a second `huggingface-safetensors` backend.** Register another
  backend, selected via `catalog.backend`. Zero interface change, but it
  duplicates the HF client and — worse — makes the user hand-match their
  *discovery* backend to their *runtime*; pick the wrong pair and browse returns
  nothing loadable.

**Recommendation: A.** Format is a property of the target runtime, not a
user-facing source choice, so deriving it from the active runtime removes a
footgun rather than adding one. (This is a genuine decision with a live
alternative — worth an explicit sign-off before coding, per our design-decision
rule.)

Either way, the **curated path stays primary and foolproof**: the setup wizard
draws only from each provider's embedded curated catalog (Piece 8), so a Metal
user is offered vetted mistral.rs models without touching online browse at all.

## Piece 6 — multi-file / directory download (genuinely new)

`ModelRecord.DownloadURLs` + the manager's shard loop already fetch several
files for a split GGUF, so **UQFF** (one, or a few, `.uqff` files) is close to
the existing path. **safetensors is the new shape:** a model is a *directory* —
`model-00001-of-0000N.safetensors` + `config.json` + `tokenizer.json` +
`tokenizer_config.json` + `generation_config.json` — and mistral.rs is pointed
at the **directory**, not a file. That means:

- `ResolveDownload`/`DownloadPlan` must enumerate the whole file manifest (not
  just weight shards), and `PrimaryFile`/`ModelRecord.Path` must denote the
  **target directory** when `Format=="safetensors"` (today `Path` is the first
  shard *file* — see the `DownloadURLs` doc comment in `types.go`).
- `manager.go` download: write each file into the model directory; "downloaded"
  = every manifest file present.
- `DeleteModel`: remove the **directory** for safetensors, not a single file.

This is the one place the existing abstractions don't already stretch. Scope it
explicitly: a `Format`-aware target (file vs directory) threaded through
`DownloadPlan` → `ModelRecord` → `manager.go` download/delete.

## Piece 7 — config + wiring

- `internal/localruntime/config/config.go`: add `MistralRSConfig` (binary path,
  model dirs, ISQ default, device/GPU-layer knobs, extra args, ready timeout),
  `Defaults()`, `applyMistralRSDefaults` — mirroring the llama-server config.
- `cmd/cercano/main.go`: construct + `RegisterProvider(mistralrs.New(...))` on
  the runtime manager next to llamaserver; register the mistral.rs engine;
  optionally warm the selected runtime.
- Runtime selection: whatever `selectOpenEngine` does for llama-server, extend
  to route the open lane to mistral.rs when it's the configured/at-hand runtime.

No `catalog.backend` change is required for Option A — the format follows the
runtime; only if we go Option B does a new backend name get registered.

## Piece 8 — curated catalog + wizard

Give mistral.rs its own embedded curated catalog (the twin of
`llamaserver/catalog.json`), RAM-tiered, listing vetted safetensors/UQFF builds
that actually load on the pinned mistral.rs build (per-platform — Metal entries
stay conservative until the upstream fixes land). The wizard, which already
draws open recommendations from the active runtime's curated set, then offers
mistral.rs models on machines where mistral.rs is the chosen runtime — no new
wizard flow, just a second curated source.

---

## What's copy vs genuinely new

| Work | Nature |
|---|---|
| Provider supervisor (`start/watch/kill/waitReady/orphans/logs`) | **Copy** from llamaserver |
| Engine adapter over `/v1` | **Copy** from engine/llamaserver (OpenAI-compatible) |
| `argsFor` (subcommand + ISQ/UQFF/gguf) | New, small |
| `mistralrscompat` gate + arch seed | New, small (data) |
| Runtime-aware gate selection in `buildCatalogDownloadRecord` | New, small, high-value |
| safetensors **directory** download + delete | **New, the real work** |
| Online safetensors discovery (Option A: `Format` on List/Detail) | New, needs a decision |
| Config + main.go wiring + curated catalog | New, mechanical |

## Phasing

1. **Provider + engine + config + curated catalog**, GGUF-format only at first
   (mistral.rs reads GGUF too) — proves the process/engine seam with zero new
   download work, reusing the existing GGUF download path.
2. **`mistralrscompat` + runtime-aware gate** — unlocks the point of the whole
   exercise (arches llama-server can't run) without yet touching downloads.
3. **safetensors/UQFF directory download** (Piece 6) — the genuinely new
   plumbing.
4. **Online safetensors discovery** (Piece 5, Option A) — last, and only if
   browse-beyond-curated is wanted for mistral.rs.

Phases 1–2 deliver "run qwen3next from a curated entry on Metal" — the headline
capability — before the hardest piece (3).

## Risks carried from the evaluation

- **Metal maturity.** The load-bearing mistral.rs Metal fixes for hybrid-MoE
  (RMSNorm correctness, a zero-buffer crash, prefill OOM) were **open PRs** at
  evaluation time. Phase 1–2 should pin a mistral.rs build and the curated Metal
  entries to what's verified; keep next-gen-MoE-on-Metal opt-in until those land.
  Re-checking those PRs' merge/release status is the cheap high-signal follow-up
  flagged in the evaluation doc.
- **Model weight/size.** `qwen3next` is an ~80B MoE; even afq2 UQFF is ~25 GB.
  The curated mistral.rs catalog must be honest about RAM tiers — this is a
  big-machine capability, not a laptop default.

## Decisions (resolved 2026-07-12)

Worked through with the design-decision protocol. These govern the Pieces above
where an inline recommendation differs.

### D1 — online safetensors discovery: format-parameterized HF backend, filter declared by the runtime

Chosen: **Piece 5 Option A**, with the format **declared by the runtime** rather
than hardcoded in the server.

- Add `Format` to `catalog.ListOptions`; the HF backend switches
  `filter=gguf`/`filter=safetensors` and reads arch from the `gguf` block or,
  when absent, `config.json` `architectures[0]`. **No `catalog.Backend`
  interface-method change** — it's a struct-field addition, transparent to the
  Ollama backend and the test fakes.
- The runtime advertises the catalog format(s) it wants on
  `RuntimeCapabilities` (ordered, e.g. mistral.rs `["uqff","safetensors"]`,
  llama-server `["gguf"]`); the server passes the active runtime's primary
  format into `List`. Runtime owns the knowledge; the catalog just filters.
- Multi-format browse from a single runtime is a later refinement — browse uses
  the primary declared format first.
- Rejected: a second `huggingface-safetensors` backend (silent config-mismatch
  footgun; the single-active registry can't browse two at once anyway) and
  runtime-owned discovery (bypasses/overloads the shipped registry).

### D2 — download layout: uniform per-model directories

Chosen: **Piece 6 Option C** (supersedes the inline Piece 6 recommendation of A).

- Every model lives in its own `modelDir/<model>/`. `ModelRecord.Path` denotes
  the model **directory**; an explicit `LoadTarget` names the file a
  file-loaded runtime opens (the `.gguf`) or the directory itself for mistral.rs.
- The download manager **loses the file-vs-dir branch**: it always writes a
  manifest of files into the model's directory (a single-file GGUF is just
  N=1). "Which file the runtime loads" is the provider's concern in `argsFor`,
  not the manager's.
- Safe on shipped code because `Discover` already recurses (`filepath.WalkDir`
  matches `.gguf` at any depth), so nesting new downloads doesn't strand legacy
  flat GGUFs — they stay discoverable. No migration is required; an optional
  lazy sweep can tidy flat files later.
- Cost lands in relocating new-download placement (`buildCatalogDownloadRecord`,
  `catalogModels`, `catalogTargetDir`/`allShardsPresent`, `manager.go` delete)
  and refreshing the multi-shard-download tests for the subdir layout.
- Rationale for choosing C over A despite touching shipped code: no real
  installed base yet (Homebrew CLI formula still pending), so the uniform layout
  is cheapest to adopt now; and C removes a branch rather than adding one, with
  every future directory-shaped format (MLX, ONNX) dropping in free.

### D3 — runtime selection: one active runtime, config'd, restart to change

Chosen: **a single active runtime**, `open_runtime: llama_server | mistralrs`
(default `llama_server`), swapped by config edit + restart. A **permanent
swap** — never two runtimes resident at once.

- The active runtime drives the browse format (D1), the compatibility gate
  (Piece 4), and serving.
- **Incompatibility nudge, in model tiering:** if the user's configured tiers
  contain models the active runtime's gate can't run, flag them in the tiering
  view with a prompt to change them — proactive, not only a download-time
  refusal. This catches tiers going silently incompatible after a runtime swap.
- Rejected: per-model auto-selection running both runtimes concurrently. It
  multiplies RAM/lifecycle complexity, fights the warm-model swap-minimization
  design, and — decisively — complicates the catalog UX: concurrent runtimes
  force a runtime selector in front of the catalog that live-reloads the whole
  model list, gate, and RAM tiers on every flip. Single-active keeps browse a
  function of one axis. Auto-selection remains a legitimate *future*; this
  decision doesn't foreclose it.
- Footprint context (approximate; verify against a pinned build before
  committing distribution): llama-server Metal is ~tens of MB incl. ggml libs
  and ships as per-OS release zips; mistralrs-server (Rust/candle) is heavier
  (~50–150 MB Metal, more with CUDA kernels) with no historical official
  prebuilt server binary — build-from-source or a self-hosted prebuilt. The
  chunkier artifact is a further reason not to keep both resident.

## Distribution (verified 2026-07-12)

Verified from mistral.rs upstream — `install.sh`, the crate manifests, and the
Docker guide. **Corrects the D3 footprint note's assumption that upstream ships
no prebuilt binary — it does.**

- **Accelerator is compile-time**, via Cargo features: `metal`, `cuda`
  (+`cudnn`, `flash-attn` SM80+, `flash-attn-v3` SM90), `mkl`, `accelerate`;
  plain CPU = no accel feature. **No runtime backend switch** — strictly one
  binary per accelerator.
- **Upstream ships prebuilt binaries.** `install.sh` fetches self-contained
  release tarballs `mistralrs-{cpu|metal|cuda<lane>-sm<cc>}-<triple>.tar.gz`
  from `releases/latest/download`, probing `nvidia-smi` to select the CUDA-lane
  × SM cell (Apple Silicon → `metal`; else `cpu`). The binary inside is a bare
  `mistralrs`; CUDA tarballs also bundle `lib/`. Also: PyPI wheels (CUDA wheels
  ~0.8–1.1 GB each) and GHCR Docker images (`ghcr.io/ericlbuehler/mistral.rs`).
- **So Cercano need not own a build matrix.** It fetches the matching upstream
  tarball on demand — the same pinned-release model already used for llama.cpp —
  reusing upstream's probe logic. `install.go` becomes fetch-and-extract, not a
  compiler; it's a *selection* problem, not a *build* problem.
- **CUDA spread** (Linux+NVIDIA tier, later): toolkit lanes 12.8–13.3; SM floor
  8.0 (Ampere) for prebuilts (older GPUs → source build); x86_64 SMs
  80/86/89/90/100/120 (+ Grace 90/100/121). ~32 published cells; a realistic
  first tier ≈ one lane × 6 x86_64 SMs — all already built upstream.
- **Phase-1 (Apple Silicon Metal) = one asset**:
  `mistralrs-metal-aarch64-apple-darwin.tar.gz`. No matrix to run.
- **Bare-binary size unconfirmed** (the ~1 GB figures are Python wheels, not the
  tarball binary) — pin against a real download before the bundling/Homebrew
  decision.
- **Naming:** the `mistralrs-server` crate is gone; workspace is `mistralrs-cli`
  + `mistralrs-server-core`, binary `mistralrs`. Update `install.go`/`argsFor`
  references in Pieces 1 and 7 from `mistralrs-server` to `mistralrs`.
