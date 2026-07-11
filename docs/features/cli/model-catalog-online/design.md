# Online model catalog — design

> **Status (2026-07-10): re-architected.** The discovery source is
> **HuggingFace's GGUF index**, not the Ollama library, and every download
> passes an **architecture compatibility gate**. This supersedes the
> Ollama-library approach that shipped on `feat/model-catalog-online`; that
> design is preserved as an appendix at the bottom. The scaffolding (on-disk
> cache, background refresher, RPCs, dashboard) is source-agnostic and is kept;
> the fetcher internals and the OCI download path are replaced in place.

## Why the change

The Ollama-library approach rested on one assumption: *"every model in the
Ollama library is verified to work in a serving loop, and its blobs are raw
GGUF both runtimes consume directly."* That was true when Ollama was a
llama.cpp wrapper. It no longer is.

- **Ollama runs its own inference engine now.** Through 2025 Ollama built a
  Go/GGML engine independent of llama.cpp. Its library therefore includes
  models whose architectures **only Ollama** can run (e.g. qwen3-next's hybrid
  linear-attention + MoE design, supported in Ollama well before llama.cpp).
- **GGUF magic bytes prove nothing about compatibility.** A file starting with
  `GGUF` is only a GGUF *container*. The `general.architecture` metadata field
  inside names the model architecture, and llama.cpp/llama-server loads **only
  the architectures compiled into the build**. An unsupported arch fails at
  load with "unknown architecture" — which is exactly the qwen-coder-3-next
  failure that triggered this rework.

So "in the Ollama library" means "works in Ollama," **not** "works in
llama-server." Since llama-server is Cercano's bundled open runtime, the
catalog must guarantee llama.cpp compatibility itself. That guarantee is the
**architecture gate**, and the clean source for it is HuggingFace.

## Chosen source: the HuggingFace GGUF index

HuggingFace is where GGUFs actually live, it has a real JSON API (no HTML
scraping), and — critically — **it exposes the GGUF architecture in the API
response**, so the gate costs one JSON fetch, not a multi-gigabyte header read.

Verified live (2026-07-10):

**List query** — `GET /api/models?filter=gguf&sort=downloads&limit=N`
returns a popularity-ranked set. Each entry carries `id`, `author`,
`downloads`, `likes`, `tags`, and `siblings[]` (the file list). Embedding
models are present (e.g. `ggml-org/embeddinggemma-300M-GGUF`), so every open
tier is coverable.

**Per-model detail** — `GET /api/models/<repo>?blobs=true` returns a `gguf`
metadata block plus per-file sizes:

```json
"gguf": {
  "architecture": "qwen2",
  "context_length": 32768,
  "chat_template": "…{%- if tools %}…<tool_call>…",
  "total": 7615616512
},
"siblings": [
  { "rfilename": "…-Q4_K_M.gguf", "size": 4683074336,
    "lfs": { "sha256": "1664fcc…" } },
  …
]
```

From this one call we get, with no file download:

| Field | Use |
|---|---|
| `gguf.architecture` | The compatibility gate input |
| `gguf.chat_template` | Tool-calling detection (scan for `tools`/`tool_call`) |
| `gguf.context_length` | Display + launch sizing |
| `siblings[].size/sha256` | Quant variants, sizes, integrity |

**Download URL** — `https://huggingface.co/<repo>/resolve/main/<rfilename>`, a
plain HTTP GET with Range support. No OCI, no manifest step.

**Noise control.** The GGUF index is large and includes broken community
quants, so browse filters to a curated **uploader allow-list** (seed:
`ggml-org`, `bartowski`, `unsloth`, plus first-party org repos like `Qwen`,
`google`) and sorts by downloads. The allow-list is a data edit, not code.

## The architecture compatibility gate

The gate is the one new primitive, shared by setup, browse, and download.

**The check:** `supportedArch.contains(archOf(model))`.

- `archOf` for an HF model = `gguf.architecture` from the API (cheap).
- `archOf` for the curated catalog or an arbitrary URL = read
  `general.architecture` from the GGUF header via an HTTP **Range** request
  (the metadata KV block lives in the header; no full download). The in-tree
  `headerIdentity` parser in `llamaserver/provider.go` already extracts
  architecture from on-disk files — it needs to also accept a remote Range
  reader.

**The supported-arch set** is derived from the **pinned llama.cpp build**, not
hand-guessed. llama.cpp enumerates its architectures in `src/llama-arch.cpp`
(`LLM_ARCH_NAMES`, arch → name string, e.g. `llama`, `qwen2`, `qwen3`,
`gemma3`, `phi3`, `deepseek2`, `bert`, `nomic-bert`). The build/vendor step
generates our allow-list from that map so the gate can never claim support the
bundled binary lacks; a Go test asserts the generated set is non-empty and
contains the archs every curated-catalog entry uses. A hand-maintained seed set
is the MVP until the codegen lands.

**Enforcement points:**

1. **Download-time hard gate (mandatory).** Before enrolling a multi-gigabyte
   download, resolve the arch and refuse if unsupported, with a clear message
   ("llama-server can't run this model's architecture (`qwen3next`); it needs
   Ollama or a newer llama.cpp"). This is the safety net that would have caught
   qwen-coder-3-next.
2. **Browse-list annotation (lazy, best-effort).** As the user inspects a
   model, the per-model fetch yields the arch; show a compatibility badge.
   Incompatible entries are shown greyed/last, not silently hidden, so the user
   understands *why* they can't pull it.

**What the gate can't catch:** a supported arch can still fail on an exotic
quant type or an unknown pre-tokenizer. Those are rare; they surface as a
graceful runtime load error, not a crash, and the model registers **absent**
(see the readiness contract in the setup-wizard design) so routing stays live.

## Two-layer catalog

Setup and browse have different needs, so two layers over the one gate:

| Layer | Source | Guarantee |
|---|---|---|
| Recommended set (setup + tiers) | Curated compatibility catalog | Verified on our build |
| Browse / advanced (`/m`) | HF GGUF index + gate | Gate blocks incompatible |

**Curated compatibility catalog.** A hand-maintained set of GGUFs we have
actually run on the pinned llama.cpp build. It moves out of the
`defaultCatalog` Go slice into an **embedded data file** (`catalog.json`,
`go:embed`) so updating models is a data edit.

The catalog is **keyed by RAM tier**, not a flat list, because the right open
model depends on the machine — what a 128 GB box runs is wrong for a 48 GB
laptop. Setup detects total memory (`sysctl hw.memsize` on macOS,
`/proc/meminfo` on Linux), picks the matching profile, shows it, and lets the
user override. Profiles: **24, 48, 96, 128 GB** (a machine takes the largest
profile at or below its RAM).

**Swap-minimization principle.** Spinning up a llama-server model loads its
full weights into memory — a multi-minute cost for the big ones. Cercano keeps
multiple models warm at once (one instance each), bounded only by RAM. So a
profile gives a tier its own distinct everyday and most-capable models **only
when both fit warm simultaneously**; otherwise they collapse to one shared
model so the hot path never pays a reload. Every profile is generated under
this rule.

Default profiles (everyday anchored on Qwen3 **general**, not Qwen3-Coder — the
newer refresh is the stronger coder; every arch is gate-supported):

| Profile | everyday | most_capable |
|---|---|---|
| 24 GB | Qwen3-14B Q4 (~9 GB) | = everyday (no swap) |
| 48 GB | Qwen3-30B-A3B-2507 Q4 (~19 GB) | = everyday (no swap) |
| 96 GB | 30B-A3B-Instruct-2507 Q4 | 30B-A3B-Thinking-2507 Q4 |
| 128 GB | 30B-A3B-2507 Q4 (~19 GB) | GLM-4.5-Air Q4 (~73 GB) |

`fast_light` and `fast_light_text` are Phi-4-mini (`phi3`), and `embedding` is
nomic-embed-text-v1.5 (`nomic-bert`, f16) across all profiles. The 96 GB
profile keeps two 30B-A3B variants (Instruct + Thinking) warm together (~37 GB,
no swap); 128 GB keeps a fast everyday plus GLM-4.5-Air warm (~92 GB).

Entry shape — note **`files[]`**, because some models ship as multi-shard
splits (GLM-4.5-Air's Q4_K_M is two files); "downloaded?" means *all* shards
present, and `size_bytes` is their sum:

```json
{
  "tier": "most_capable",
  "repo": "unsloth/GLM-4.5-Air-GGUF",
  "files": [
    "Q4_K_M/GLM-4.5-Air-Q4_K_M-00001-of-00002.gguf",
    "Q4_K_M/GLM-4.5-Air-Q4_K_M-00002-of-00002.gguf"
  ],
  "arch": "glm4moe",
  "quant": "Q4_K_M",
  "size_bytes": 72975748384,
  "supports_tools": true
}
```

This is the **only** source the setup wizard touches, which is what makes the
guided path foolproof. A test asserts every profile entry's `arch` is in the
supported set.

**HF browse** is the `/m` power-user surface: the gated HuggingFace index, for
anything not in the curated set.

## Quant selection

HF repos ship many quants (IQ2 … Q4_K_M … Q8_0 … f16). Default to **`Q4_K_M`**
when present (the quality/size sweet spot), else the nearest 4-bit K-quant. The
`/m` browse offers a quant picker sourced from `siblings[]` with sizes; setup
takes the curated entry's pinned `file` and does not prompt.

## Tool-calling detection

The `everyday` tier drives agent tool-calling, so it must be a tool-capable
model. Detect support by scanning `gguf.chat_template` for `tools` /
`tool_call` markers; surface it as a badge in browse and as the
`supports_tools` flag in the curated catalog. Setup's everyday-tier default is
always a tool-capable pick.

## Code changes — replace in place

The `feat/model-catalog-online` scaffolding stays; the source and download
internals are swapped. Inventory:

**`internal/ollamacatalog/` → rename to `internal/modelcatalog/`.**

- **Replace** `catalog.go`'s `Fetcher`: HTML scraping (`ListModels`,
  `ListTags`) and OCI machinery (`FetchManifest`, `ModelLayer`, `DownloadBlob`,
  `DownloadBlobRange`, the manifest/layer structs) are **deleted**. New fetcher
  calls the HF JSON API: `ListModels()` → list query filtered to the uploader
  allow-list; `ModelDetail(repo)` → the `gguf` block + `siblings`.
- **Keep** `cache.go` (atomic write, TTL, staleness) and `manager.go`'s cache
  loader + background refresher unchanged in shape — they don't care what the
  fetcher talks to. `Resolve` (the OCI `OCIResolver` impl) is **deleted**;
  HF download URLs are plain `resolve/main` GETs.

**Download manager (`internal/localruntime/`).**

- **Delete** the `OCIResolver` seam and the `OllamaRef` just-in-time
  manifest→blob resolution in `manager.go`. Every download is now a plain HTTP
  GET of a `resolve/main` URL through the existing DownloadURL path (which
  already handles Range resume).
- `DownloadRequest` loses `OllamaRef`; entries carry a `DownloadURL` again.

**`llamaserver/provider.go`.**

- `defaultCatalog` Go slice → `go:embed catalog.json`, parsed into the
  per-tier curated set.
- Add the **supported-arch set** + the gate helper (`archSupported`), reused by
  download enrollment and browse annotation.
- Extend `headerIdentity` parsing to accept a remote Range reader for the
  non-HF gate path.
- Provider **registration readiness = GGUF on disk** (`Discover` reports
  `downloaded`): a not-yet-downloaded tier registers **absent** so
  `dispatch/select.go` crosses to cloud during the download window. (This is the
  routing contract the setup-wizard design depends on; it lands here.)

**RPCs / config.** `GetOnlineCatalog` / `RefreshOnlineCatalog` names and the
`catalog-cache.json` location are unchanged. `ListRuntimeModels` still merges
curated + browse with `catalog_updated_at`. The download-time gate adds one
refusal path with a typed error the CLI renders.

## Testing

- **Fetcher** against an `httptest` server returning canned HF JSON (list +
  per-model detail); no live network in CI.
- **Gate**: `archSupported` true/false around the boundary; a curated-catalog
  validity test that every entry's `arch` is in the supported set and every
  everyday entry `supports_tools`.
- **Download refusal**: an unsupported-arch enrollment returns the typed
  refusal, enrolls nothing.
- **Header Range-read** parses `general.architecture` from a truncated GGUF
  header fixture.
- **Manual runbook** lists the live HF URLs to spot-check occasionally.

## Open items (not blocking the spec)

- **Codegen for the supported-arch set** from the pinned `llama-arch.cpp` vs
  the hand-maintained seed. Seed ships first; codegen is a follow-up that makes
  the gate authoritative.
- **Uploader allow-list growth** — data edits as trusted quant authors emerge.

## Decisions (2026-07-10)

- **Source = HuggingFace GGUF index, gated** (Option C). Ollama dropped as a
  catalog source; still allowed as a post-setup external server.
- **Architecture gate** on `gguf.architecture` vs a supported set derived from
  the pinned llama.cpp build; enforced hard at download time, annotated lazily
  in browse.
- **Two layers**: curated compatibility catalog (embedded data file, setup's
  only source) + gated HF browse.
- **Replace in place**: rename `ollamacatalog` → `modelcatalog`, swap the
  fetcher to the HF API, delete the OCI/manifest/blob path and the `OllamaRef`
  download branch; keep cache, refresher, RPCs, dashboard.
- **Readiness = GGUF on disk** lands in the provider here, satisfying the
  setup-wizard routing contract.
- **Quant default `Q4_K_M`**; curated catalog pins exact files, browse offers a
  picker from `siblings[]`.

---

## Appendix: superseded Ollama-library approach

The original design (shipped on `feat/model-catalog-online`) sourced discovery
from `ollama.com/library` (HTML-scraped) and downloaded GGUF blobs from
`registry.ollama.ai` over the OCI protocol, on the rationale that the Ollama
library was a free compatibility matrix and its blobs were raw GGUF. That
rationale broke when Ollama's own inference engine diverged from llama.cpp (see
"Why the change"), so Ollama-library models are no longer guaranteed to load in
llama-server. The shipped pieces and their fate under the re-architecture:

- `ollamacatalog` package (fetcher, OCI blob client, cache) → renamed to
  `modelcatalog`; fetcher + OCI path replaced, cache kept.
- `OCIResolver` + JIT manifest→blob resolution + `OllamaRef` download field →
  deleted (HF uses plain `resolve/main` URLs).
- Cache (24h TTL at `~/.config/cercano/catalog-cache.json`), background
  refresher, `RefreshOnlineCatalog` RPC, dashboard freshness footer + refresh
  → kept as-is.
- The deferred "tag/quant picker" is now the HF quant picker sourced from
  `siblings[]`; the deferred "HF escape hatch" is subsumed — HF *is* the source.

The full original text lives in git history for this file prior to 2026-07-10.
