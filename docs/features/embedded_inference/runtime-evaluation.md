# Local Inference Runtime Evaluation

> **Status (2026-07-11): evaluated, with live investigation folded in.** This is
> the runtime bake-off that the `embedded_inference` and `model-catalog-online`
> designs assumed existed but never did. It re-opens the runtime selection
> because the bundled runtime — `llama-server` (llama.cpp) — structurally cannot
> run several next-generation model architectures, `qwen3-coder-next` being the
> triggering case. Two sub-agent investigations (compact-quant availability +
> Metal stability, and the catalog-integration surface) are incorporated below.

## Why re-evaluate

Cercano bundles `llama-server` as its owned, supervised open runtime
(`internal/localruntime/llamaserver`). It is mature, lightweight, GGUF-native,
OpenAI-compatible, and has excellent Metal support. But it has one structural
limit that just became load-bearing:

**llama.cpp loads only the model architectures compiled into the build.** A new
architecture — especially ones with novel attention (hybrid linear-attention,
SSM/Mamba layers) or novel MoE routing — cannot run until someone writes and
merges a C++ kernel and the build ships it. `qwen3-coder-next` (the `Qwen3-Next`
architecture: Gated-DeltaNet linear attention + MoE) is the case that surfaced
this. (Note: llama.cpp *has since* gained `qwen3next` support and GGUFs exist —
see Findings — but the lag is the structural problem, and it recurs with every
genuinely new architecture.)

The `model-catalog-online` rework (HuggingFace GGUF index + an architecture
**compatibility gate**) was the *design* response. But that gate is a
*workaround*: it formalizes the limitation by refusing to download models
llama.cpp can't run. It does not let us run next-gen models. The strategic fix
is a better runtime. **Caveat discovered during this evaluation: that rework is
not actually in the code yet — see "Reality check on the code" below.**

## The deciding axis: platform target

The single most clarifying variable is **what the local runtime is for**.
Cercano targets **macOS, Linux, and Windows** desktops as daily-driver hosts —
not a datacenter GPU fleet. That reshapes the candidate field:

- Datacenter serving engines (**vLLM**, **SGLang**) are CUDA/NVIDIA-first. Their
  value is high-concurrency GPU throughput. On a Mac laptop they are a
  non-starter; on Windows they are painful. They are relevant only to a *future
  self-hosted Linux+NVIDIA tier*, not the bundled desktop runtime.
- The engines that solve "run next-gen models on my machine" across
  mac/linux/windows are **mistral.rs**, **MLX** (Apple-only), and pragmatically
  **Ollama** (already integrated as an external endpoint).

So the question is not "llama.cpp vs vLLM." It is "what replaces or complements
llama-server as the bundled cross-platform runtime, and what tier does each
other engine occupy."

## Candidate field (the wider net)

| Runtime | Platforms (prebuilt) | Next-gen arch velocity | Native formats | Server / API | Tool calling | Maturity | Sidecar fit |
|---|---|---|---|---|---|---|---|
| **llama.cpp / llama-server** *(current)* | mac (Metal), linux (CUDA/Vulkan/CPU), windows (CUDA/Vulkan/CPU) | **Lags** — arch must be compiled in | GGUF only | OpenAI-compatible | via templates | ★★★★★ | ★★★★★ (current) |
| **mistral.rs** | mac (Metal), linux (CUDA/CPU), **windows (CPU only)** | **Near day-one** — HF `architectures` auto-detect | safetensors+ISQ, UQFF, GGUF, GPTQ, AWQ | OpenAI **and** Anthropic `/v1/messages` | native, grammar/strict-schema + MCP client | ★★★☆☆ (single primary author, on Candle) | ★★★★☆ (drop-in `serve`) |
| **Ollama** | mac/linux/windows | **Fast** — own Go/GGML engine | GGUF (own store) | own + OpenAI-compat | native | ★★★★☆ | ✗ external product, we don't own it |
| **MLX / mlx-lm** | **mac only** (Apple Silicon) | Fast on Apple | MLX (safetensors-derived) | OpenAI-compat server | partial | ★★★☆☆ | ★★★☆☆ (Python, Apple-only) |
| **vLLM** | linux (CUDA); mac experimental | Day-one (HF transformers) | safetensors, GPTQ/AWQ | OpenAI-compat | native | ★★★★★ (datacenter) | ✗ CUDA/Python weight |
| **SGLang** | linux (CUDA) | Day-one | safetensors | OpenAI-compat | native | ★★★★☆ | ✗ CUDA/Python weight |
| **ExLlamaV2 + TabbyAPI** | linux/windows (CUDA) | Medium | EXL2/GPTQ | OpenAI-compat | via templates | ★★★☆☆ | ✗ NVIDIA-only |
| **ONNX Runtime GenAI** | cross-platform | **Weak** — limited model set | ONNX | library | manual | ★★★☆☆ | ✗ arch coverage |
| **TensorSharp (.NET)** | cross-platform (.NET) | **Weak / unproven** | tensor lib | none | none | ★☆☆☆☆ (experimental) | ✗ no .NET in stack |

*(Star ratings are directional judgement, not measured scores.)*

## Deep dive: mistral.rs

Author `EricLBuehler/mistral.rs`, built on HuggingFace's Candle Rust ML
framework. Runtime facts below are **verified against the current source /
README** (2026-07-11).

**Runs qwen3-coder-next — verified.** The loader-type registry
(`mistralrs-core/src/pipeline/loaders/normal_loaders.rs`) maps HF model classes
to loaders, including verbatim `"Qwen3NextForCausalLM" => Ok(Self::Qwen3Next)`
(→ `Qwen3NextLoader`). The same enum carries `DeepSeekV3`, `Qwen3Moe`,
`GLM4Moe`, `GptOss`, `GraniteMoeHybrid`, `Lfm2Moe`, `HunYuanMoEV1` — a very
current set. An `AutoNormalLoader` reads `architectures[0]` from the model's
`config.json` and selects the loader automatically — the structural reason it
tracks new architectures near day-one instead of waiting on hand-ported kernels.

**Distribution fits our sidecar model.** `mistralrs serve` is a single binary
exposing an OpenAI-compatible `/v1` **and** an Anthropic-compatible
`/v1/messages` endpoint. Prebuilt self-contained binaries install via a shell
script with no Rust/CUDA toolkit (Metal on Apple Silicon; CUDA or CPU on Linux;
CPU on Windows), plus GHCR docker images. Maps almost 1:1 onto our supervised-
sidecar design (spawn child, health-probe HTTP endpoint, restart policy).

**Capabilities we gain:** native tool calling (grammar-enforced, strict-schema),
a built-in MCP client, in-situ quantization (ISQ — run any HF model without a
published GGUF), multimodality, and the Anthropic-compat endpoint (our
`internal/llm/anthropic` adapter could target a local mistralrs instance).

**Performance (project v0.8.2 benchmarks):** beats llama.cpp on prefill and is
comparable-to-better on decode; vLLM still substantially outperforms it on
large-MoE BF16 datacenter throughput — why vLLM stays a datacenter-tier option.

## Findings from investigation (2026-07-11)

### The triggering model is an ~80B MoE — the size floor is set by the model, not the format

`Qwen/Qwen3-Coder-Next` is an **~80B-parameter MoE** (GGUF metadata: `total`
≈ 79.67B, 512 experts, 10 active/token). A **<10 GB** build is mathematically
impossible (<1 bit/param). Realistic floors, computed from HuggingFace blob
byte-counts:

| Path | Smallest usable build | Size | mistral.rs status |
|---|---|---|---|
| **UQFF** (mistral.rs native) | `mistralrs-community/Qwen3-Coder-Next-UQFF` afq2 (2-bit) | **~25 GB** | ✅ native format, reliable |
| **GGUF** (1-bit class) | `unsloth/Qwen3-Coder-Next-GGUF` UD-TQ1_0 | **~19 GB** | ⚠️ mistral.rs GGUF loader for `qwen3next` is an **open, unmerged PR** (#2049/#2129) — llama.cpp/LM Studio territory today |
| **Structurally pruned** | `lovedheart/…REAP-40B-A3B-GGUF` | smaller | trades capability for size |

**Implication:** adopting mistral.rs does not make qwen3-coder-next a
laptop-friendly download. The reliable native route is a **~25 GB UQFF** pull
(and comparable RAM to run). The broader arch-lag argument for a better runtime
still holds — it just isn't demonstrated by a model most desktops can run
comfortably. Confidence: **high** (sizes computed from authoritative HF API).

### mistral.rs on Metal is mature for mainstream models, immature for next-gen hybrid-MoE

The base Metal backend is a first-class, long-stable target. But for **exactly
the Qwen3-Next-class hybrid-MoE models this evaluation is about**, Metal
stabilization is **active and largely unmerged** as of this snapshot:

| PR/issue | State | Metal signal |
|---|---|---|
| #2206 zero-element KV buffers | open PR | hard crash on hybrid (no-KV GDN) layers |
| #2201 Qwen3.6 RMSNorm/AFQ/hybrid-KV | open PR | **correctness**: ~2.1× over-scale → ~14× MoE blow-up (silent wrong output) |
| #2208 Metal paged chunked prefill | open PR | long-prompt **OOM/swap** (36 GB Mac → swap) + GDN conv-state panic |
| #2049/#2129 qwen3-next GGUF loaders | open PR | GGUF quantized path for this arch not yet merged |

These are external-contributor PRs, reported working on Apple Silicon *after*
patching — i.e. you'd ship an unreleased build to get a stable next-gen-MoE
experience on Metal today. Real reproduced failure modes: buffer-index crashes,
long-prompt OOM (Cercano's coding prompts are long — this path is directly
relevant), and numerically wrong output. Confidence: **medium-high**; the live
uncertainty is **merge/release timing** — re-check #2201/#2206/#2208/#2049
before committing to a build pin.

### Reality check on the code (from the integration investigation)

The `model-catalog-online` design reads as shipped/re-architected, but the code
is **still at the pre-rearchitecture state**:

- package is still `internal/ollamacatalog/` (HTML-scrapes `ollama.com/library`
  + OCI `FetchManifest`/`DownloadBlob`), **not** the HF JSON fetcher;
- **there is no architecture gate anywhere** — `archSupported` does not exist;
- the curated set is still the two-entry `defaultCatalog` Go slice, not
  `go:embed catalog.json`;
- the selection config field is **`open_runtime`** (default `ollama`), not the
  `local_runtime` the docs describe;
- the setup wizard still runs the old `StepPrimary → StepCloud → StepLocus →
  StepTiers` machine.

**Consequences for this evaluation:** (1) the compatibility gate I expected to
"mirror" for mistral.rs is **greenfield** — building it for mistral.rs (a JSON
`architectures[0]` check) is actually easier than the not-yet-built GGUF-header
gate; (2) a mistral.rs runtime plugs in at the **stable `localruntime.Provider`
seam** and can proceed **independently** of the unfinished HF catalog rework;
(3) the `model-catalog-online` design doc should be re-flagged as *design, not
shipped*.

## What we lose vs llama-server

1. **Format inversion + heavier downloads.** llama-server is GGUF-only;
   mistral.rs is safetensors+ISFirst with UQFF/GGUF as compact options. The
   reliable compact path for next-gen models is UQFF (~25 GB for the 80B
   flagship), and the arch gate must move from GGUF-header parsing to a
   `config.json` `architectures[0]` check (simpler, but new).
2. **Maturity / bus factor**, and specifically **next-gen-MoE-on-Metal
   immaturity** (open PRs above). For a *load-bearing bundled runtime* this is
   the sharpest risk.
3. **Windows GPU.** Prebuilt Windows is **CPU-only**; mac (Metal) and linux
   (CUDA) are prebuilt-GPU. llama.cpp has better prebuilt Windows GPU coverage.

## Recommendation

Adopt the **platform-tiered runtime** shape (Option D), sequenced to respect the
maturity findings:

- **Add mistral.rs as a second bundled provider now**, at the stable
  `localruntime.Provider` seam, running **alongside** llama-server (not a cutover).
  This unlocks next-gen architectures llama.cpp can't load.
- **Keep llama-server as the default/primary near-term.** It is more mature,
  has prebuilt Windows GPU, and is the safer default while mistral.rs's
  next-gen-MoE-on-Metal fixes (#2201/#2206/#2208) are unmerged.
- **Treat next-gen-hybrid-MoE-on-Metal as experimental** until those upstream
  PRs land in a pinned release; gate it behind an explicit opt-in and pin a
  known-good mistral.rs version.
- **vLLM / SGLang** → future self-hosted **Linux + NVIDIA** server tier only.
- **MLX** → optional Mac-maximum-performance provider, later.
- **Ollama** → remains a supported external endpoint (not a bundled runtime).
- **TensorSharp / ONNX Runtime GenAI** → dropped.

## Integration surface (summary of the code investigation)

The `localruntime.Provider` seam holds; a `mistralrs` provider drops in beside
`llamaserver` with the supervisor, orphan-reaping, and OpenAI-compatible engine
adapter **copyable in shape**. Three genuinely new pieces:

1. **Arch gate** — mirror mistral.rs's `NormalLoaderType` class set as a Go
   allow-list; gate on `config.json` `architectures[0]` (greenfield — no gate
   exists today).
2. **Multi-file safetensors download** in the shared `localruntime/manager.go`
   (`runDownload` is single-URL today; a sharded repo is N files + a directory
   delete — the one invasive change, with "delete a directory" as the sharpest
   hazard).
3. **ISQ / subcommand launch args** (`mistralrs serve plain -m … --isq …` /
   `gguf` / `uqff`) needing real-binary validation on Metal.

Config + startup wiring is small: new `MistralRSConfig`, extend the
`open_runtime` selector to a third value, register the provider in
`buildRuntimeManager` (`cmd/cercano/main.go`). `dispatch/select.go` needs **no
change** — the "readiness = on-disk" routing contract is satisfied at the
provider's `Discover` (for a repo, "all shards present"). The engine adapter
reuses the OpenAI-compatible `/v1` path (the Anthropic `/v1/messages` route is a
later, bigger option). Files-to-add / files-to-change detail lives with the
integration investigation.

## Open questions

1. **Merge/release status of mistral.rs PRs #2201, #2206, #2208, #2049** — if
   landed and released, the next-gen-MoE-on-Metal verdict softens materially and
   the "experimental/opt-in" gating can relax. *(Worth one follow-up check
   before pinning a version.)*
2. **UQFF loader support in the pinned mistral.rs version** — the UQFF repos
   exist; loader support must match the version Cercano ships.
3. **Windows V1 posture** — is CPU-only acceptable for Windows, with
   llama-server as the Windows-GPU fallback?
4. **Sequencing vs the unfinished HF catalog rework** — the mistral.rs provider
   is independent of it, but the curated-catalog / setup-wizard touch-points
   should wait for the HF rework (and the wizard's locus-first redesign) to land.

## References

- mistral.rs: https://github.com/EricLBuehler/mistral.rs — loader registry
  `mistralrs-core/src/pipeline/loaders/normal_loaders.rs`; Metal PRs
  #2201/#2206/#2208/#2049; docs https://ericlbuehler.github.io/mistral.rs/
- UQFF builds: `huggingface.co/mistralrs-community` (Qwen3-Coder-Next-UQFF, …)
- GGUF builds: `huggingface.co/unsloth/Qwen3-Coder-Next-GGUF`,
  `huggingface.co/Qwen/Qwen3-Coder-Next-GGUF`
- Current runtime layer: `source/server/internal/localruntime/` (llamaserver
  provider); download machinery in `manager.go`
- Catalog rework (design, **not yet shipped**):
  `docs/features/cli/model-catalog-online/design.md`
- Runtime abstraction design: `docs/features/embedded_inference/README.md`
- Engine abstraction: `docs/features/engine/agnosticism.md`
