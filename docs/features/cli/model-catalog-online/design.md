# Online model catalog — design

> **Status (2026-07-12): pluggable catalog backends — shipped on `main`.** Model discovery is
> abstracted behind a **backend interface** with exactly one **active backend**
> at a time, selected in config. **HuggingFace** is the default active backend —
> the real home of GGUF files, a JSON API that exposes the model architecture,
> and plain resumable downloads. **Ollama** is retained as a selectable backend
> (we may support more backends later). Every download *into llama-server*
> passes an **architecture compatibility gate**, whichever backend it came from.
> This supersedes the earlier "replace Ollama in place" plan: Ollama is not
> removed — it becomes one implementation of the backend interface.

## Why a gate, and why pluggable

The original catalog rested on one assumption: *"every model in the Ollama
library works in a serving loop, and its blobs are raw GGUF both runtimes
consume directly."* That was true when Ollama was a llama.cpp wrapper. It no
longer is.

- **Ollama runs its own inference engine now.** Through 2025 Ollama built a
  Go/GGML engine independent of llama.cpp. Its library therefore includes models
  whose architectures **only Ollama** can run (e.g. qwen3-next's hybrid
  linear-attention + MoE design, supported in Ollama well before llama.cpp).
- **GGUF magic bytes prove nothing about compatibility.** A file starting with
  `GGUF` is only a GGUF *container*. The `general.architecture` metadata field
  inside names the model architecture, and llama.cpp/llama-server loads **only
  the architectures compiled into the build**. An unsupported arch fails at load
  with "unknown architecture" — exactly the qwen-coder-3-next failure that
  triggered this rework.

Two consequences shape the design:

1. **Compatibility is the invariant, not the source.** Whatever backend offers
   a model, only a model llama.cpp can actually load may be downloaded into
   llama-server. So the gate lives at the consumer and protects every backend
   uniformly.
2. **The source should be swappable.** HuggingFace is the best default (arch in
   the API, plain downloads), but Ollama has value and other backends will come.
   So discovery sits behind an interface with one active backend.

## The backend interface

A backend is any source of downloadable models. Exactly one is active at a
time (config-selected; default `huggingface`); browse and search use the active
backend only — no cross-backend merging.

```go
// Backend is a source of downloadable GGUF models (HuggingFace, Ollama, …).
type Backend interface {
    Name() string                                          // "huggingface" | "ollama"
    List(ctx context.Context, opts ListOptions) ([]Model, error)
    Detail(ctx context.Context, id string) (Detail, error) // files, arch, tools, sizes
    ResolveDownload(ctx context.Context, id, file string) (DownloadPlan, error)
}

type Model  struct { Backend, ID, Author string; Downloads, Likes int }
type Detail struct { Backend, ID, Architecture string; SupportsTools bool; ContextLength int; Files []File }
type File   struct { Name string; SizeBytes int64 }

// DownloadPlan is what the download manager consumes: concrete URLs (one, or
// several for a sharded split) plus the primary filename and total size.
type DownloadPlan struct { URLs []string; PrimaryFile string; TotalBytes int64 }
```

- **HuggingFace backend** = the `modelcatalog` HF client. `List`/`Detail` hit
  the HF JSON API; `ResolveDownload` returns the plain `resolve/main` URL(s).
- **Ollama backend** = today's `ollamacatalog`, adapted. `List`/`Detail` use the
  library; `ResolveDownload` does the OCI manifest→blob resolution **inside the
  backend** and returns the blob URL.

A small registry holds the available backends and the active selection.

## The HuggingFace backend (default active)

HuggingFace exposes the GGUF architecture in its API, so the gate costs one JSON
fetch, not a multi-gigabyte header read. Verified live (2026-07-10):

**List** — `GET /api/models?filter=gguf&sort=downloads&direction=-1&limit=N` →
a popularity-ranked set (`id`, `downloads`, `likes`, `siblings[]`).

**Detail** — `GET /api/models/<repo>?blobs=true` → a `gguf` block plus per-file
sizes:

```json
"gguf": { "architecture": "qwen2", "context_length": 32768,
          "chat_template": "…{%- if tools %}…<tool_call>…" },
"siblings": [ { "rfilename": "…-Q4_K_M.gguf", "size": 4683074336 }, … ]
```

| Field | Use |
|---|---|
| `gguf.architecture` | The compatibility gate input |
| `gguf.chat_template` | Tool-calling detection (scan for `tools`/`tool_call`) |
| `gguf.context_length` | Display + launch sizing |
| `siblings[].size` | Quant variants and sizes |

**Download URL** — `https://huggingface.co/<repo>/resolve/main/<file>`, a plain
resumable GET. No OCI, no manifest.

**Noise control.** The GGUF index is large and full of broken community quants,
so the HF backend filters to a curated **uploader allow-list** (seed: `ggml-org`,
`bartowski`, `unsloth`, `nomic-ai`, and first-party orgs `Qwen`, `google`,
`microsoft`, `mistralai`, `meta-llama`) and sorts by downloads. The allow-list
is a data edit.

Status: implemented as `internal/modelcatalog` (`Client` with `ListModels` /
`ModelDetail` / `DownloadURL` / `Compatible`), httptest-covered.

## The Ollama backend (retained)

`ollamacatalog` is kept and adapted to the `Backend` interface. Its
Ollama-specific machinery — library scraping, and OCI manifest→blob resolution —
moves behind `List`/`Detail`/`ResolveDownload`. Crucially, the OCI resolution
that used to live in the download manager (the `OCIResolver` seam) moves **into
this backend's `ResolveDownload`**, so the download manager no longer knows
anything about OCI or Ollama.

## The architecture compatibility gate

The gate is applied by the **consumer** (the server, when preparing a download
into llama-server) against the arch the active backend reports — so it protects
every backend, and a future backend gets it for free.

**The check:** `llamacompat.Supported(detail.Architecture)`.

- HF backend: arch comes from `gguf.architecture` (cheap).
- Ollama backend: arch comes from the GGUF header it reads while resolving.
- Curated catalog / arbitrary URL: read `general.architecture` from the GGUF
  header via an HTTP **Range** request (metadata KV block lives in the header;
  no full download). The in-tree `headerIdentity` parser already extracts arch
  from on-disk files; it needs to also accept a remote Range reader.

**The supported-arch set** is derived from the **pinned llama.cpp build**, not
hand-guessed. llama.cpp enumerates architectures in `src/llama-arch.cpp`
(`LLM_ARCH_NAMES`). A build/vendor step should generate our allow-list from that
map so the gate can never claim support the binary lacks; a hand-maintained seed
(`internal/llamacompat`) is the MVP, with a Go test asserting every curated-
catalog entry's arch is in it.

**Enforcement points:**

1. **Download-time hard gate (mandatory).** Before enrolling a multi-gigabyte
   download, resolve the arch and refuse if unsupported, with a clear message
   ("llama-server can't run this model's architecture (`qwen3next`) — switch the
   catalog backend to Ollama or wait for a newer llama.cpp").
2. **Browse-list annotation (lazy, best-effort).** As the user inspects a model,
   `Detail` yields the arch; show a compatibility badge. Incompatible entries
   are greyed/last, not hidden, so the user understands *why*.

**What the gate can't catch:** a supported arch can still fail on an exotic
quant or unknown pre-tokenizer. Rare; it surfaces as a graceful load error and
the model registers **absent** (setup-wizard routing contract) so routing stays
live.

Status: gate implemented as `internal/llamacompat` (seed set + `Supported`),
tested.

## Two layers: curated setup set + active-backend browse

| Layer | Source | Guarantee |
|---|---|---|
| Recommended set (setup + tiers) | Curated compatibility catalog | Verified on our build |
| Browse / advanced (`/m`) | Active backend + gate | Gate blocks incompatible |

The **curated compatibility catalog** is a fixed, embedded set of GGUFs we have
run on the pinned build — the setup wizard's only source, independent of which
browse backend is active (its download URLs are HF `resolve/main` links baked
in). It lives in `internal/localruntime/llamaserver/catalog.json` (`go:embed`),
**keyed by RAM tier**, because the right open model depends on the machine.
Setup detects total memory (`sysctl hw.memsize` / `/proc/meminfo`), picks the
largest profile at or below it (24 / 48 / 96 / 128 GB), shows it, and lets the
user override.

**Swap-minimization principle.** Spinning up a llama-server model loads its full
weights into memory (multi-minute for the big ones); Cercano keeps multiple
models warm at once, bounded by RAM. So a profile gives a tier distinct everyday
and most-capable models **only when both fit warm simultaneously**; otherwise
they collapse to one shared model so the hot path never reloads.

Default profiles (everyday anchored on Qwen3 **general**, not Qwen3-Coder — the
newer refresh is the stronger coder; every arch gate-supported):

| Profile | everyday | most_capable |
|---|---|---|
| 24 GB | Qwen3-14B Q4 (~9 GB) | = everyday (no swap) |
| 48 GB | Qwen3-30B-A3B-2507 Q4 (~19 GB) | = everyday (no swap) |
| 96 GB | 30B-A3B-Instruct-2507 Q4 | 30B-A3B-Thinking-2507 Q4 |
| 128 GB | 30B-A3B-2507 Q4 (~19 GB) | GLM-4.5-Air Q4 (~73 GB) |

`fast_light` and `fast_light_text` are Phi-4-mini (`phi3`); `embedding` is
nomic-embed-text-v1.5 (`nomic-bert`, f16) across all profiles. Entries carry
**`files[]`** for multi-shard splits (GLM-4.5-Air's Q4_K_M is two files);
"downloaded?" means *all* shards present and `size_bytes` is their sum.

Status: catalog + loader (`ProfileForRAM`, multi-shard `DownloadURLs`) +
validity test implemented and green.

## Downloads are backend-agnostic

The download manager fetches a `DownloadPlan`'s URLs — one file, or several for
a sharded split — with `.part` resume and cancel. It does **not** know about OCI
or Ollama: each backend's `ResolveDownload` hands it plain URLs (the Ollama
backend having done its manifest→blob resolution internally). The multi-shard
download path is implemented (`downloadShard` loop, all-shards-present state).

The `OCIResolver` seam and the `OllamaRef` branch that used to live in
`internal/localruntime/manager.go` have been **removed** (commit `85008643`) —
their job moved into the Ollama backend's `ResolveDownload`.

## Proto: a generic catalog id

Today three messages (`RuntimeModel`, `DownloadRuntimeModelRequest`,
`GetModelRAMEstimateRequest`) carry an Ollama-specific `ollama_ref` field. In a
pluggable world that name is wrong for non-Ollama backends. It is replaced by a
**generic catalog id** — the model's id within whatever backend is active — so a
download request means "fetch catalog id X" and the active backend resolves it.
This is a `.proto` change: edit the schema, regenerate, and update the callers
(`agentclient`, the server's browse/download/RAM-estimate handlers). Doing it
properly (not bolting a discriminator onto the old field) is the intended
non-shortcut path.

## Config

A single setting selects the active backend:

```yaml
catalog:
  backend: huggingface   # huggingface (default) | ollama
```

Unset → `huggingface`. The RAM-estimate subsystem and browse both read the
active backend from here.

## Quant selection & tool detection

HF repos ship many quants; default to **`Q4_K_M`** when present, else the
nearest 4-bit K-quant. Browse offers a quant picker from `Detail.Files`; setup
uses the curated entry's pinned file and does not prompt. Tool capability is
detected from the chat template (`tool_call` / `<tools>`) and surfaced as a
badge in browse and the `supports_tools` flag in the curated catalog; the
everyday tier's default is always tool-capable.

## Testing

- **HF backend** against an httptest server with canned HF JSON (list + detail)
  — done.
- **Gate** boundary + curated-catalog validity (every entry's arch supported,
  agent tiers tool-capable) — done.
- **Multi-shard download** end to end (both shards land, cumulative bytes,
  delete clears all) + single-file back-compat — done.
- **Ollama backend** `ResolveDownload` (OCI manifest→blob) against an httptest
  registry — done (`ollamacatalog/ollama_backend_test.go`).
- **Download refusal** on unsupported arch — done
  (`server/download_resolve_test.go::TestBuildCatalogDownloadRecord_GatesUnsupportedArch`
  asserts `qwen3next` is refused before any download; a compatible arch carries
  through in `…_Compatible`).
- **Header Range-read** parses `general.architecture` from a truncated fixture —
  not needed by the shipped backends (HF reads arch from JSON; Ollama from the
  header while resolving). Only an arbitrary-URL / local-import path would need
  it; deferred until that feature exists.

## Status & remaining work

**Shipped on `main`** (merged from `feat/model-catalog-hf-gate`, 2026-07-11):
the whole pluggable-backend design above is implemented and tested.

- `internal/catalog` — `Backend` interface + concurrency-safe `Registry`
  (first-registered-active, fail-loud `SetActive`).
- `internal/modelcatalog` — HuggingFace backend (default active): trusted-
  uploader allow-list, JSON arch read, plain `resolve/main` URLs, shard-group
  expansion.
- `internal/ollamacatalog` — Ollama adapted to `Backend`; the OCI
  manifest→blob resolution moved into its `ResolveDownload`. The `OCIResolver`
  seam and `OllamaRef` branch are gone from the download manager (commit
  `85008643`).
- Proto `ollama_ref` → `catalog_id` across `RuntimeModel`,
  `DownloadRuntimeModelRequest`, `GetModelRAMEstimateRequest` (commit
  `a2eacc48`); `agentclient` and the browse/download/RAM-estimate handlers
  updated.
- Server rewired onto the active backend: `ListRuntimeModels` browses it,
  `DownloadRuntimeModel` → `buildCatalogDownloadRecord` gates + resolves it,
  `GetModelRAMEstimate` reads it. The active backend is chosen by the
  `catalog.backend` config field (default `huggingface`), wired in `main.go`.
- `llamacompat` gate wired into the download path; curated RAM-tiered catalog +
  loader; multi-shard downloads with resume; open-tier readiness = GGUF on disk.

**Genuinely open:**

- Generalize warmed-RAM estimates and catalog freshness beyond the Ollama cache
  — per-selection `GetModelRAMEstimate` works now, but the eager warmed list and
  the "catalog updated Nh ago" label return generically only once a
  backend-neutral catalog cache lands (see the note in `ListRuntimeModels`).
- Auto-generate the `llamacompat` allow-list from the pinned llama.cpp
  `LLM_ARCH_NAMES` at build/vendor time; today it's a hand-maintained seed.
- Remote Range-read of `general.architecture`, only if an arbitrary-URL or
  local-import path is added (the shipped backends don't need it).

## Decisions

### 2026-07-11 — pluggable backends

- **Discovery is pluggable behind a `Backend` interface; one active backend at
  a time, config-selected, default HuggingFace.** No cross-backend merging.
- **Ollama is retained** as a backend (not removed); its OCI resolution moves
  into the backend's `ResolveDownload`, making the download manager
  backend-agnostic.
- **The gate lives at the consumer** and applies to every backend's downloads
  into llama-server.
- **The proto `ollama_ref` field is generalized** to a backend-agnostic catalog
  id (regenerated), rather than kept as an Ollama-specific field.

### 2026-07-10 — carried forward

- **Architecture gate** on the model's arch vs a set derived from the pinned
  llama.cpp build; hard at download time, annotated in browse.
- **Curated compatibility catalog**, RAM-tiered (24/48/96/128 GB),
  swap-minimizing, everyday anchored on Qwen3 general; the setup wizard's only
  source.
- **Readiness = GGUF on disk** lands in the provider.
- **Quant default `Q4_K_M`**; curated pins exact files, browse offers a picker.
