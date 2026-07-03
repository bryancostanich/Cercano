# Online model catalog — design

## Why

Cercano's llama-server local runtime needs GGUF model files to run. The
current story is:

- A hardcoded catalog of ~10 curated models in
  `source/server/internal/localruntime/llamaserver/provider.go`
  (`defaultCatalog`).
- Direct HuggingFace download URLs per entry.
- A `DownloadRuntimeModel` RPC that streams progress.
- A runtime dashboard (`Cmd+M` / `/m`) with catalog search + download.

The gap: users who don't find their model in the ~10-entry list have no
in-app path to discover one, and there's no refresh — new models don't
appear until someone rebuilds Cercano.

## Chosen source: Ollama's library

- `https://ollama.com/library` — the human-browsing catalog. ~236 model
  families as of research (July 2026).
- `https://registry.ollama.ai` — the OCI registry backing the library.
  Standard OCI protocol, no auth for public models.

Why Ollama over HuggingFace:

1. **Compatibility matrix**: Ollama is one of Cercano's two supported
   local runtimes. Every model in the library is verified to work in a
   serving loop. HuggingFace has no equivalent quality signal.
2. **Blob compatibility**: Ollama's model layers are raw GGUF files
   (verified: first 4 bytes are the "GGUF" magic). Both runtimes
   consume them directly — no conversion, no repackaging.
3. **No daemon dependency**: The public registry serves everything over
   HTTPS. A user on the llama-server runtime can pull from ollama's
   registry without ever installing the ollama daemon.
4. **Curation is free**: We inherit Ollama's editorial choices rather
   than curating our own list from HuggingFace's firehose.

## What ships (already checkpointed)

The `ollamacatalog` package at
`source/server/internal/ollamacatalog/` — chunk 1 of this feature. Two
files:

### `catalog.go`

- `Fetcher` — HTTP client with:
  - `ListModels()` → HTML-parses `ollama.com/library` for family links.
  - `ListTags(name)` → HTML-parses the family page for tag links.
  - `FetchManifest(name, tag)` → returns the OCI manifest struct.
  - `DownloadBlob(name, digest)` → returns a `ReadCloser` on the GGUF.
  - `DownloadBlobRange(name, digest, offset)` → resumable via HTTP Range.
- `Model`, `Manifest`, `ManifestLayer` structs.
- `Manifest.ModelLayer()` picks the layer with mediaType
  `application/vnd.ollama.image.model` (the GGUF); rejects manifests
  with no such layer.
- `BaseURL` / `RegistryURL` overridable for tests.

### `cache.go`

- `Cache` struct with `FetchedAt`, `Source`, `Models`.
- `Load(path)`, `(*Cache).Save(path)`, `(*Cache).IsStale(now, ttl)`.
- Atomic write via tempfile + rename — a crash mid-write never leaves
  a corrupt file.
- Missing cache file returns `nil, nil` (not an error) — callers treat
  "no cache yet" as "empty cache".

### Tests

12 tests covering HTML parsing, manifest handling, blob streaming +
Range resume, and cache atomic-write / staleness. All green.

## Remaining work (chunks 2-5)

### Chunk 2: Server RPC integration

**Decision to make**: eager vs lazy tag+manifest fetching.

- **Eager** (fetch all tags + manifests once, cache): ~1000+ requests
  (236 families × ~5 tags average × 2 requests each for tag list +
  manifest). Slow initial fetch (minutes). Simple UX afterward.
- **Lazy** (fetch family list up front, tags on drill-in, manifest on
  download): ~1 request initially. Instant UX. Multi-second delay when
  user clicks a family (fetch tags) or clicks download (fetch manifest).
- **Hybrid** (fetch family list + first-page tags eagerly, rest lazy):
  balances but adds complexity.

Recommendation: **lazy with visible loading states**. Cache the family
list aggressively (24h TTL). Fetch tags + manifest just-in-time with a
one-second spinner. Users don't browse-then-abandon nearly as often as
they browse-then-decide, so the total round-trip count is low.

**Two RPCs to add**:

```proto
rpc GetOnlineCatalog(GetOnlineCatalogRequest) returns (GetOnlineCatalogResponse) {}
rpc RefreshOnlineCatalog(RefreshOnlineCatalogRequest) returns (RefreshOnlineCatalogResponse) {}
```

- `GetOnlineCatalog`: reads the cache. Returns families + FetchedAt
  timestamp. Never hits the network — the server's background refresher
  keeps the cache warm.
- `RefreshOnlineCatalog`: force-fetch. Blocks until done. Returns the
  new cache. Used by the dashboard's `R` (refresh) key.

**Background refresher**: goroutine kicked off at server startup that
runs `Fetcher.ListModels()` if the cache is stale, sleeps for 1 hour
between checks. On failure, keeps serving old cache and logs.

### Chunk 3: Wire OCI blob download into `DownloadRuntimeModel`

The existing `Manager.DownloadModel` uses direct HTTP GET of a
DownloadURL field. Replace (or augment) that with a two-step flow:

1. If ModelRecord's DownloadURL is a registry URL → parse the
   `name@tag` from ID → `Fetcher.FetchManifest` → `Fetcher.ModelLayer`
   → `Fetcher.DownloadBlobRange` at the model's known-downloaded-bytes
   offset (for resumption).
2. If DownloadURL is a plain HTTP URL → keep the existing flow.

Progress reporting flows through the existing `ModelRecord.DownloadedBytes` /
`DownloadTotalBytes` fields — no wire-protocol changes needed.

### Chunk 4: CLI dashboard changes

- **Timestamp line**: at top of catalog block, `Catalog updated 2h ago`.
- **`R` key**: forces `RefreshOnlineCatalog`. Shows spinner while
  refreshing.
- **Stale color**: timestamp goes muted after 7 days, error-colored
  after 30. Never blocks use — always usable if we have any cache.
- **Two catalog sections?**: The existing hardcoded catalog can stay
  as-is; the online catalog appears as a second section below it, or
  they merge (with online taking priority for duplicate family names).
  Recommendation: merge, dedupe by family name, prefer online (which
  has fresher tag lists).

### Chunk 5: Install modal "Browse models" button

The small piece. In `open_runtime_modal.go`:

- `runtimeModalIdle` state when `status.Missing == "model"`: replace
  `[Enter] Install now` with `[Enter] Browse models`. Enter dismisses
  the modal and opens the runtime dashboard.
- `runtimeModalNeedsModel` state: add `[Enter] Browse models` as
  primary action alongside the existing `[Esc] Close`.

Wire-up: emit a `openRuntimeDashboardMsg` (following the pattern of
existing modal-triggered navigations) and let Model's `Update` handle
the transition.

## Testing strategy

- **Unit tests** for the fetcher and cache (done in chunk 1).
- **Unit tests** for the manager's OCI-download path with an httptest
  server standing in for the registry (chunk 3).
- **CLI tests** for the dashboard's timestamp rendering + refresh-key
  handling + spinner (chunk 4).
- **No live-network tests**: the fetcher's tests use httptest servers
  entirely. A separate manual-verification runbook can list the live
  URLs to spot-check occasionally.

## Configuration

Nothing new required. The cache lives at
`~/.config/cercano/catalog-cache.json` alongside the other per-user
state (config, telemetry, permissions, crash log). TTL is hardcoded to
24 hours; if user demand for a different TTL emerges, add it to the
`llama_server` config block.

## Open questions

- **HF escape hatch**: still worth building? For niche models not in
  Ollama's library. Not blocking; can ship online-catalog first and add
  HF-URL paste later.
- **Live progress reporting during refresh**: acceptable to block the
  UI while refreshing? Alternative: refresh in background, show
  timestamp updating in real time.

## What's checkpointed

`2f8b02b4 feat(server): ollamacatalog package — online catalog fetcher,
OCI blob client, on-disk cache` on branch `feat/model-catalog-online`.

Next session picks up chunk 2.
