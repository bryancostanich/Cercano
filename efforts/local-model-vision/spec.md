# Local-model vision: wire mmproj, and never send images to text-only models

## The failure that started this

A cloud turn carrying a screenshot hit a transient Anthropic connection reset,
failed over to the local `llama_server` backend, and died:

```
stream error: ... tool loop error: openai busy (500): ... image input is not
supported — hint: if this is unexpected, you may need to provide the mmproj
```

The transient-retry half of this is already fixed (commit `12d0d2a4`). What
remains are two independent, load-bearing gaps this error exposed:

1. **Local vision models don't actually work.** `llama-server` serves an image
   only when launched with `--mmproj <projector.gguf>`. A case-insensitive
   search for `mmproj`/`projector` across `source/server` returns **nothing** —
   the flag is never passed, the projector file is never downloaded, and no
   model metadata records that a model is a vision model. So even a genuine
   vision GGUF can't see images today.

2. **The local backend lies about its capabilities.** `openai.Client.
   Capabilities()` hardcodes `SupportsVision: true`
   (`internal/llm/openai/client.go:83`). The local `llama_server` backend is
   served through this exact client, so every local model claims vision. When an
   image reaches a text-only local model (directly, or via failover), the
   backend rejects it with the 500 above — there is no capability gate anywhere
   in the request path to prevent it.

## What this effort delivers

Both halves, staged so the bug-fix plumbing lands before the model hunt:

- **Vision-capable local models get fully wired.** A curated model can declare a
  multimodal projector; the projector is downloaded as a companion file and
  passed to `llama-server` as `--mmproj`; the backend then truthfully reports
  vision support and accepts images end-to-end.
- **Images are never sent to a model that can't see them.** When the active
  target lacks vision, image content is stripped to a text stub at the outbound
  serialization seam — the same graceful degradation the tool loop already
  applies to tool-result images — with a user-visible "image omitted" notice.
  This fixes the 500 for both direct-to-local and failover paths.

## Design decisions (settled with the user before planning)

**D1 — mmproj discovery: explicit catalog field.** A projector is curated truth,
exactly like `SupportsTools`/`ExtraArgs`. `CuratedModel` gains an `MmprojFile`
(a filename within the model's `Files`); it rides the existing multi-file
download machinery. `ModelRecord` carries the resolved `MmprojPath`. Filesystem
sibling-scan for arbitrary user-supplied GGUFs is explicitly deferred — the
catalog is already "curated, verified" and browsing arbitrary HF GGUFs is a
separate gated path.

**D2 — truthful `SupportsVision`: dedicated field, threaded through Config.**
`CuratedModel` and `ModelRecord` gain a `SupportsVision bool`, set in the
catalog alongside `MmprojFile` — mirroring the existing
`SupportsChat`/`SupportsEmbed`/`SupportsTools` triplet exactly (curated boolean,
not inferred from a path). It threads into a new `openai.Config.SupportsVision`
→ `Client` field, reported by `Capabilities()`. Cloud providers keep passing
`true` explicitly; nothing changes for them.

**D3 — text-only target gets images stripped, not blocked.** Reuse the existing
pattern (`[N image(s) omitted: the active model has no vision support]` from the
tool loop). Stripping happens at the **outbound conversion seam in the
openai/responses clients** — the one place every request funnels through — gated
on the client's `SupportsVision` flag, so it covers direct-to-local and failover
uniformly. A one-line user-visible progress notice accompanies a strip so an
image-less answer isn't mysterious. Blocking the turn (worse UX) and
failover-only guards (leaves direct-to-local broken) were rejected.

**D4 — staged, plumbing-first, then add a real curated vision model.** Land all
plumbing with unit tests first (fixes the 500, wires `--mmproj`), then add one
curated vision GGUF + its mmproj and prove it end-to-end: launch with
`--mmproj`, send an image, get a non-500 answer. Staging isolates the risky/slow
model hunt at the end — if it stalls, the bug-fix plumbing has already landed.

## Scope

**In:**
- `MmprojFile` on `CuratedModel`; `MmprojPath` on `ModelRecord`; download +
  resolution of the companion projector file.
- `--mmproj` launch arg in the llama-server provider's `argsFor`.
- `SupportsVision` on `CuratedModel`/`ModelRecord`; threaded into
  `openai.Config`/`Client`/`Capabilities()`; set correctly for the local
  provider in `server.go`.
- Image-stripping at the openai/responses outbound seam, gated on
  `SupportsVision`, with a user-visible notice.
- One curated vision model + mmproj in `catalog.json`, size-tiered, with
  end-to-end verification.

**Out:**
- Filesystem sibling-scan / GGUF-metadata autodetect for user-supplied vision
  GGUFs (deferred; D1).
- mistral.rs / ollama vision wiring (llama-server is the backend in the failing
  path; the capability-gate half protects the others by making them truthful,
  but their launch-side vision wiring is a separate effort).
- Any change to cloud providers' vision behavior.

## Acceptance criteria

1. A text-only local model no longer 500s on an image: the image is stripped to
   the documented stub, the turn completes, and the user sees an "image omitted"
   notice. Verified for both direct-to-local and cloud→local failover.
2. `openai.Client.Capabilities().SupportsVision` reflects the active local
   model's real capability, not a hardcoded `true`. Cloud providers unchanged.
3. A curated vision model launches `llama-server` with `--mmproj <path>`,
   reports `SupportsVision: true`, and accepts an image end-to-end (non-500,
   coherent answer referencing the image).
4. `go build ./...`, `go vet ./...`, and `go test ./...` are green in
   `source/server` at every phase boundary.
