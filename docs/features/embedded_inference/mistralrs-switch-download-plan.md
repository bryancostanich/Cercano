# mistral.rs runtime-switch → readiness chip → auto-download — plan

Status: proposed (grounded in code as of `main` @ `9f223fc2`)
Branch context: the bulk of this feature already shipped to `main`; this plan
covers the **remaining pull-path bug** and pins down the intended behavior so we
stop re-deriving it.

## What already exists on `main` (verified, do not rebuild)

- `1fdc5020 feat(mistralrs): light open-runtime chip + auto-fetch default on switch`
  - **Server push path** (`server.go` UpdateConfig switch-site, ~line 931):
    switching `open_runtime → mistralrs` calls
    `autoDownloadMistralRSDefault(ctx, c)` (enqueues the curated default model's
    download, idempotent + non-blocking) and broadcasts
    `buildMistralRSStatus(c, mistralRSModelMissing(ctx, c))`.
  - `buildMistralRSStatus` (`events.go:192`): pure formatter. `modelMissing=false`
    → `ok=true`; `modelMissing=true` → `ok=false, Missing="model"`, with a
    distinct message for "no default configured" vs "not downloaded".
  - `resolveMistralRSDefault` / `mistralRSModelMissing` / `autoDownloadMistralRSDefault`
    (`server.go:229/262/281`): resolve the configured default against the runtime
    manager's inventory using the same fuzzy `MatchesModel` the provider uses at
    Start, so readiness agrees with what actually launches.
  - **Observer push path** (`runtime_observer.go:87`): re-broadcasts
    `buildMistralRSStatus` when the runtime inventory changes (e.g. a download
    finishes), so the chip clears on its own.
  - **CLI chip** (`renderOpenRuntimeChip`, `model.go:4280`): runtime-aware label —
    `Missing=="model"` + `Runtime=="mistralrs"` → "⚠ mistral.rs model not
    downloaded (F1)"; else the existing llama-server text.
- `6fe4b73f fix(cli): persist runtime switch to llama_server when already ready`
  - Fixed the Runtime-tab picker dropping a switch TO llama_server when already
    ready. ollama/mistralrs targets were already correct. **Not part of this bug.**
- Runtime **dashboard** (`runtime_dashboard.go`) already renders rich
  `downloading NN%` action rows off `DownloadState`. The status **chip** is a
  separate, coarser surface and intentionally binary (ok / needs-setup).

## The remaining bug (root cause, confirmed)

`GetOpenRuntimeStatus` — the **pull path** used at CLI startup and by the
switch-gate probe — does NOT mirror the push path for mistralrs.

`source/server/internal/server/open_runtime_install.go:87`

```go
if runtime != "llama_server" {
    // Ollama and future runtimes don't need setup surfacing today — we
    // return an ok=true snapshot so the client hides the chip.
    return &proto.GetOpenRuntimeStatusResponse{
        Status: buildOpenRuntimeStatus(runtime, cfg, nil),
    }, nil
}
```

For `runtime == "mistralrs"` this returns an **unconditional `ok=true`** snapshot.
Consequences:

1. **Fresh CLI startup** with `open_runtime=mistralrs` and the default model NOT
   downloaded → the startup pull returns ok=true → the "(F1)" chip is hidden even
   though the model is missing. The push-path event that *would* have shown it
   already fired before the CLI connected, so nothing corrects it until the next
   inventory change.
2. **Switch-gate probe**: `openRuntimeSwitchCmd` / settings-page path probes
   `GetOpenRuntimeStatus("mistralrs")` before switching; it always sees ok=true,
   so it can never surface "will need to download" pre-switch the way llama_server
   surfaces "will need to install".

This is a pull/push asymmetry, not missing machinery — the helpers the push path
uses (`mistralRSModelMissing`, `buildMistralRSStatus`) already exist and are the
correct call for the pull path too.

## Fix

Route `mistralrs` in `GetOpenRuntimeStatus` to the same formatter the push path
uses. Single-file change, no proto/CLI change required (the chip already knows
how to render `Runtime=="mistralrs" && Missing=="model"`).

```go
switch runtime {
case "mistralrs":
    return &proto.GetOpenRuntimeStatusResponse{
        Status: buildMistralRSStatus(cfg, s.mistralRSModelMissing(ctx, cfg)),
    }, nil
case "llama_server":
    // ... existing Detect path ...
default:
    // ollama + future runtimes: ok=true snapshot, chip hidden.
    return &proto.GetOpenRuntimeStatusResponse{
        Status: buildOpenRuntimeStatus(runtime, cfg, nil),
    }, nil
}
```

Required companion change: the handler currently signs `_ context.Context` and
calls `mistralRSModelMissing` needs a real ctx for the inventory lookup. Change
the signature to `ctx context.Context` and thread it through (the push path uses
the request ctx; here use the handler's ctx). `resolveMistralRSDefault` already
takes a ctx and tolerates cancellation.

## Behavior contract (the thing we kept re-deriving — pin it here)

Chip is intentionally **binary**: ok (hidden) vs needs-setup (amber "(F1)").
It is NOT a progress meter — progress lives in the `/m` runtime dashboard.

| open_runtime | default model on disk? | chip (pull AND push) |
|---|---|---|
| ollama | n/a | hidden (ok=true) |
| llama_server | binary+GGUF present | hidden |
| llama_server | binary or GGUF missing | "⚠ llama-server not installed / no GGUF (F1)" |
| mistralrs | present | hidden (ok=true) |
| mistralrs | missing / none configured | "⚠ mistral.rs model not downloaded (F1)" |

Switch flow for mistralrs (unchanged, already correct):
1. User switches `open_runtime → mistralrs` in /config.
2. Server persists, auto-enqueues the curated default download (non-blocking),
   broadcasts a not-downloaded chip.
3. CLI shows the amber chip; the `/m` dashboard shows `downloading NN%`.
4. Download finishes → observer re-broadcasts ok=true → chip clears on its own.

With THIS fix, a CLI that (re)connects mid-download or starts cold also gets the
correct not-downloaded chip from its startup pull, instead of a false ok.

## The `o: downloading` chip state (REQUIRED — not out of scope)

The chip is NOT purely binary. It has THREE states for mistralrs:

| server state | chip |
|---|---|
| default model `Downloaded` | hidden (ok) |
| default model `Downloading` | **`o: downloading`** (in-progress style, NO "(F1)") |
| default absent / `not_downloaded` / `failed` / no default | "⚠ mistral.rs model not downloaded (F1)" |

No percent in the chip — the `/m` dashboard owns progress. But the chip MUST
distinguish "sit tight, it's pulling" from "you need to act (F1)". Today
`mistralRSModelMissing` collapses `Downloading` into the F1 bucket, which is the
bug: a switch or a mid-download reconnect wrongly nags the user to press F1.

The server already knows: `DownloadState` is first-class
(`localruntime/state.go`: `Downloading` / `Downloaded` / ...), and the observer
already re-broadcasts on every state change — so `Downloading → Downloaded`
already fires a fresh event; we just need the wire + chip to carry the state.

### Layers for the downloading state

1. **Proto** — `message OpenRuntimeStatus` (`agent.proto:932`) gains
   `bool downloading = 8;` (new tag, backward-compatible). Regen
   `agent.pb.go`/`agent_grpc.pb.go`.
2. **Server** — replace the boolean collapse with the real state:
   - `resolveMistralRSDefault` already returns the full `ModelRecord` (has
     `DownloadState`). Keep it.
   - `buildMistralRSStatus(cfg, state DownloadState)` (change signature from
     `modelMissing bool`): `Downloaded` → `ok=true`; `Downloading` →
     `ok=false, downloading=true, missing="", message="mistral.rs default model
     downloading…"`; anything else → `ok=false, missing="model"` (the F1 path).
   - Update all three call sites to pass the state instead of a bool: push site
     (`server.go:~931`), observer (`runtime_observer.go`), and the pull site
     (`GetOpenRuntimeStatus`, the asymmetry fix above). A tiny helper
     `mistralRSDefaultState(ctx, cfg) DownloadState` (returns `not_downloaded`
     when unresolved) keeps the three sites identical.
3. **CLI chip** (`renderOpenRuntimeChip`, `model.go:4280`) — when
   `status.Downloading` is true, render `o: downloading` in an in-progress
   style (Muted/BorderDim, not the amber Primary "(F1)" style). `ok` still
   hides. Only when `Missing=="model"` and NOT downloading show the "(F1)"
   text.

## Explicitly out of scope

- No percent/progress bar IN THE CHIP — the word "downloading" only; progress
  lives in the `/m` dashboard.
- llama_server persist path — already fixed by `6fe4b73f`.

## Test plan

- Unit: table test on `GetOpenRuntimeStatus` for `runtime=mistralrs` with (a)
  default present in a fake inventory → `Status.Ok==true`; (b) default missing →
  `Ok==false, Missing=="model"`; (c) no default configured → `Ok==false,
  Missing=="model"` + the "no default" message. Mirror the existing
  `buildMistralRSStatus` unit tests' fixtures.
- `go build ./... && go test ./...` in `source/server` and `source/clients/cli`.

## One-file change surface

- `source/server/internal/server/open_runtime_install.go` — the `GetOpenRuntimeStatus`
  handler: `_ context.Context` → `ctx context.Context`; add the `mistralrs` case.
- test file alongside it.
