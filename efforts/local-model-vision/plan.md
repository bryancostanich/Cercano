# Plan: local-model vision — wire mmproj + gate images on capability

Staged (D4): land the capability-gate + mmproj plumbing with unit tests first
(this fixes the 500 and wires `--mmproj`), then add a real curated vision model
and prove it end-to-end. Each phase must keep `go build ./...`, `go vet ./...`,
and `go test ./...` green in `source/server` before the next begins.

Seams confirmed during planning recon (file:line):
- `internal/llm/provider.go` — `Capabilities{ SupportsVision bool }` (exists).
- `internal/llm/openai/client.go:83` — `Capabilities()` hardcodes
  `SupportsVision: true`; `Config` struct (~:14) has no vision field; `NewClient`
  (~:34) builds the `Client`.
- `internal/llm/openai/*` + `internal/llm/responses/*` — outbound content-block
  → wire conversion (where image blocks serialize; the strip seam).
- `internal/loop/toolloop.go:203,577` — existing `toolResultBlocks(..,
  supportsVision)` stub `[N image(s) omitted: the active model has no vision
  support]` — the pattern to mirror.
- `internal/localruntime/llamaserver/catalog.go` — `CuratedModel` struct + the
  embedded `catalog.json`.
- `internal/localruntime/types.go:82` — `ModelRecord` (has `SupportsChat/Embed/
  Tools`, `ExtraArgs`, `Path`, `Files`/shard fields).
- `internal/localruntime/llamaserver/provider.go:530` — `argsFor` builds the
  launch args (`--model`, `--ctx-size`, `--gpu-layers`, then per-model
  `ExtraArgs`); `:497` `exec.Command`.
- `internal/server/server.go:3277` — a provider-construction site already does
  `SupportsVision: c.SupportsVision`; the local-provider construction is where
  the record's vision flag must reach `openai.Config`.

## Phase 1 — Capability gate: stop sending images to text-only local models

The bug fix. Make the local backend truthful and strip images when it can't see
them. No mmproj yet — after this phase, text-only local models degrade
gracefully instead of 500ing.

- [ ] Add `SupportsVision bool` to `openai.Config` and the `openai.Client`
  struct; wire it in `NewClient`. Change `Capabilities()` to report
              `SupportsVision: c.supportsVision` instead of hardcoded `true`.
- [ ] Do the same for the `responses` client (`internal/llm/responses`), which
  is the other OpenAI-compatible local path.
- [ ] At the outbound content-block conversion seam, when `!supportsVision`,
  replace image blocks with the text stub `[N image(s) omitted: the active
              model has no vision support]` (the exact phrasing already at
              `agent/toolloop.go:208`). LOCAL SURPRISE (recon): tool-RESULT images are
              already gated a layer up in `agent/toolloop.go:577` via
              `Capabilities().SupportsVision` — making capability truthful (above) makes
              that gate effective for local for the first time. The UNGATED path is
              CONVERSATION/user-turn images, serialized in `openai/adapter.go:46` /
              `resolveImageURLs`. So the strip is a pre-pass (`stripImagesForTextOnly`)
              called in openai `Chat`+`StreamChat` (mirroring the `resolveImageURLs`
              call site), keeping `messagesToOpenAI` pure.
- [ ] NOTICE — LOCAL SURPRISE (recon): the house behavior (`toolResultBlocks`,
  toolloop.go:213) does NOT emit a separate progress banner — it folds the
              `[N image(s) omitted...]` stub into the message text as the user-visible
              signal (it shows in the transcript). Matching that is more consistent than
              a bespoke notice, so conversation-image stripping is stub-only too; the
              separate `⚠ ...` progress event is dropped. A banner is a one-line runner
              add later if wanted.
- [ ] LOCAL provider SupportsVision is threaded in the engine's `clientFor`
  (`engine/llamaserver/llmprovider.go`), NOT `server.go:3277` (that site is a
              different provider path). Phase 1 defaults it false (images stripped —
              safe); Phase 2 threads the real per-model flag via `endpointFor`. Cloud
              openai/responses pass true explicitly (done in `cloudfactory/factory.go`);
              mistral.rs local passes false (vision wiring out of scope).
- [ ] Tests: unit-test that an image request to a `SupportsVision:false` client
  serializes the stub (not an image block) and that a `true` client passes
              the image through. Cover both openai and responses clients.
- [x] Build + vet + full `go test ./...` green.
- [x] Checkpoint.

## Phase 2 — Catalog + record: `MmprojFile` / `MmprojPath` / `SupportsVision`

Add the curated metadata and thread the record's vision flag into the provider
so `server.go` reports real capability.

- [ ] Add `MmprojFile string` and `SupportsVision bool` to `CuratedModel`
  (`catalog.go`), with doc comments matching the `SupportsTools`/`Files`
              style. `MmprojFile` is a filename that must also appear in `Files` so the
              download manager fetches it.
- [ ] Add `MmprojPath string` and `SupportsVision bool` to `ModelRecord`
  (`types.go`), populated where `CuratedModel` → `ModelRecord` is built
              (resolve `MmprojPath` to the on-disk companion path, mirroring how `Path`
              is resolved from `Files`).
- [ ] Point `server.go`'s local-provider construction at
  `record.SupportsVision` (replacing the Phase-1 default-false), so a vision
              model now reports `true` and a text-only model reports `false`.
- [ ] Tests: catalog round-trips `MmprojFile`/`SupportsVision`; record
  resolution produces the right `MmprojPath`; a model with no mmproj yields
              empty `MmprojPath` and `SupportsVision:false`.
- [x] Build + vet + full `go test ./...` green.
- [ ] Checkpoint.

## Phase 3 — Launch: pass `--mmproj` to llama-server

Wire the projector into the actual process launch.

- [ ] In `argsFor` (`provider.go:530`), when the record has a non-empty
  `MmprojPath`, append `--mmproj <path>` to the launch args (before per-model
              `ExtraArgs`, consistent with existing ordering).
- [ ] Guard: only emit `--mmproj` when the file exists on disk; log a clear
  warning and fall back to text-only (leave `SupportsVision` effectively
              false) if the projector is declared but missing, rather than launching a
              broken server.
- [ ] Tests: `argsFor` includes `--mmproj <path>` iff `MmprojPath` set and file
  present; omits it otherwise.
- [x] Build + vet + full `go test ./...` green.
- [ ] Checkpoint.

## Phase 4 — Add a curated vision model + end-to-end verification

The staged, higher-risk finish: a real model that proves the whole chain.

- [ ] Select a vision GGUF + mmproj pair verified to load on the pinned
  llama.cpp build (candidates: a Qwen2.5-VL or Gemma-3 vision GGUF at a
              sensible RAM tier). Confirm the projector filename and repo.
- [ ] Add the model to `catalog.json` with `Files` including the projector,
  `MmprojFile` set, `SupportsVision:true`, correct size/tier, and any
              required `ExtraArgs`.
- [ ] Download it via the normal path; confirm the projector lands alongside the
  weights and the model counts as downloaded only when both files present.
- [ ] End-to-end: launch it (assert `--mmproj` in the live args), send a request
  with an image, and confirm a non-500, coherent response that references the
              image. Capture the transcript in HANDOFF.md.
- [ ] Regression: confirm a text-only local model still strips + notices (Phase 1
  behavior intact) and cloud vision still works unchanged.
- [ ] Build + vet + full `go test ./...` green.
- [ ] Checkpoint.

## Verification strategy

- Phases 1–3 are unit-testable in isolation and gated on the full suite.
- Phase 4 is the only phase requiring a live server + real model; its
  verification is manual/end-to-end and recorded in HANDOFF.md.
- Delegation note: recon and any bulk tracing should go to open models via
  `dispatch` with scoped, testdata-excluding searches — the planning pass showed
  unscoped Grep floods context with `real_conversation.json`. When the local
  runtime is healthy enough to serve dispatch, prefer it for mechanical passes.

## Risks / open questions

- **Model hunt (Phase 4)** is the schedule risk: a GGUF+mmproj pair that loads
  clean on the pinned build must be found and download-tested. If it stalls, the
  bug-fix (Phases 1–3) has already landed and Phase 4 can become a fast follow.
- **responses client image path**: recon showed its mid-stream errors bypass
  normalize; confirm during Phase 1 exactly where its image blocks serialize so
  the strip lands at the right seam.
- **Notice plumbing**: confirm the outbound client seam can emit a
  progress/stream event (or whether the notice must be raised one layer up in the
  runner/loop). If the client can't cleanly emit, raise the notice at the loop
  seam that already knows `supportsVision`.
