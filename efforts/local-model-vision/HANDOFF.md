# Handoff: local-model vision / mmproj / image gating

## Summary

This effort fixed the original failure mode and wired local vision plumbing:

- Transient cloud errors now retry the same provider once before failover.
- Text-only local models no longer receive image blocks; images are stripped to
  the standard omission stub before a no-vision backend can 500.
- Curated llama-server models can declare `supports_vision` and `mmproj_file`.
- llama-server launches vision models with `--mmproj <projector.gguf>` when the
  projector is present.
- GLM-4.5V was added as the first curated GLM vision entry and downloaded.

## Commits

- `12d0d2a4` — retry transient provider errors on same provider before failover.
- `da6c91e` — gate images on model capability; stop 500 on text-only local models.
- `48a8193` — carry mmproj/vision metadata from catalog through to provider capability.
- `3894297` — pass `--mmproj` to llama-server for vision models.
- `01dc062` — add GLM-4.5V catalog entry with mmproj.

## Current runtime state after cleanup

Default local model restored to GLM-4.5-Air:

```yaml
models:
  open:
    overrides:
      llama_server:
        everyday: glm-4.5-air-q4_k_m
```

Only Air is running after cleanup:

```text
llama-server --model ~/.cercano/models/glm-4.5-air-q4_k_m/GLM-4.5-Air-Q4_K_M-00001-of-00002.gguf ...
```

GLM-4.5V is stopped. This avoids Air+V co-residency, which is not safe on this machine.

GLM-4.5V files are present:

```text
~/.cercano/models/glm-4.5v-q4_k_m/GLM-4.5V-Q4_K_M.gguf          ~66G
~/.cercano/models/glm-4.5v-q4_k_m/mmproj-GLM-4.5V-Q8_0.gguf    ~948M
```

Manual download helper files were removed after the final model file landed.

## GLM-4.5-Air finding

Concrete answer: GLM-4.5-Air is text-only. It is not a model that can be made
vision-capable by loading a projector.

Evidence:

- Upstream repo is text-generation.
- Installed/catalog files contain no `mmproj`.
- Chat template handles text content only.
- Raw llama-server + Air + image reproduces the original 500:

```text
image input is not supported - hint: if this is unexpected, you may need to provide the mmproj
```

Cercano's fixed OpenAI client path protects Air: with `SupportsVision:false`, the same image-bearing request is stripped to the omission stub and succeeds instead of 500ing.

## GLM-4.5V finding

GLM-4.5V is the GLM family vision model, not "Air plus vision." It has a separate
GGUF and a separate projector:

```json
{
  "repo": "ggml-org/GLM-4.5V-GGUF",
  "files": [
    "GLM-4.5V-Q4_K_M.gguf",
    "mmproj-GLM-4.5V-Q8_0.gguf"
  ],
  "supports_vision": true,
  "mmproj_file": "mmproj-GLM-4.5V-Q8_0.gguf"
}
```

It launched with `--mmproj` and health was OK:

```text
llama-server --model ~/.cercano/models/glm-4.5v-q4_k_m/GLM-4.5V-Q4_K_M.gguf \
  --mmproj ~/.cercano/models/glm-4.5v-q4_k_m/mmproj-GLM-4.5V-Q8_0.gguf \
  --ctx-size 32768 ...

/health => {"status":"ok"}
```

A tiny image request returned HTTP 200, proving the `--mmproj` plumbing works and
that the backend no longer rejects image input as unsupported.

A follow-up stability pass found issues:

- One larger generated red-square request returned a llama-server `500 Compute error` / empty reply.
- During testing, the agent resurrected GLM-4.5-Air because the active local
  model override still pointed at Air. That caused Air+V co-residency until the
  config was corrected and V was stopped.

Conclusion: GLM-4.5V is a valid curated vision entry and proves the plumbing, but
it is not yet recommended as the default local model. Keep Air as the default
text/reasoning model.

## RAM notes

Observed Air RSS with 32k context and q8 KV cache:

```text
~67.4 GiB resident
```

Expected GLM-4.5V:

- Main weights: ~65.6 GiB
- Projector: ~0.93 GiB
- KV/runtime/image overhead: roughly 3–10 GiB depending on request/context
- Practical RSS expectation: ~70–78 GiB
- Safe headroom: ~85–90 GiB free unified memory before launch

Do not run Air and V together.

## Important cleanup already done

- Restored local open model override to Air:

```text
local_model=glm-4.5-air-q4_k_m
open_default_model=~/.cercano/models/glm-4.5-air-q4_k_m/GLM-4.5-Air-Q4_K_M-00001-of-00002.gguf
```

- Stopped GLM-4.5V.
- Left only Air running.
- Removed manual download helper files.

## Recommended next effort: vision-as-tool

The right long-term design is not to make GLM-4.5V the default. Instead:

- Keep GLM-4.5-Air as the reasoning / agent model.
- Add a separate lightweight vision model tier.
- Expose the vision model as a tool, e.g. `inspect_image(image_id, question)`.
- Air receives image placeholders and can ask targeted visual questions through the tool.
- Tool answers are cached and returned as text, then Air continues reasoning.

This avoids loading a 70GB vision model for normal turns and keeps the stronger text model in control.

Potential candidate families for the lightweight vision tier:

- Gemma-3-4B vision GGUF + mmproj
- Qwen2.5-VL 3B/7B GGUF + mmproj
- SmolVLM / Moondream-class models for low-RAM image inspection

Open design questions for the next effort:

1. Store image attachments by stable `image_id` in the conversation / request state.
2. Add a built-in `inspect_image` tool whose implementation calls a configured vision provider.
3. Decide whether the vision tool is local-only or can also be used by cloud-primary turns.
4. Add routing/capability config for a `local_vision` model tier separate from `local_text` / everyday.
5. Cache `(image_id, question)` results.
6. Decide fallback behavior when no local vision model is available.

## Verification commands used

- Full server gates after code changes:

```bash
cd source/server
go vet ./...
go test ./...
```

- Runtime safety check:

```bash
pgrep -fl 'llama-server'
```

- GLM-4.5V health when loaded:

```bash
curl http://127.0.0.1:52046/health
```

- Air negative control:
  raw image request to Air returned the original 500 unsupported-image error.

- Air protected path:
  Cercano OpenAI client with `SupportsVision:false` stripped the image and returned non-500.

## 2026-08-13 follow-up: local vision candidates and pasted-image export

This follow-up tested practical lightweight local vision candidates for the
`inspect_image` tool path after the vision-as-tool work landed.

### Pasted-image export gap fixed

Problem found during model-quality testing: pasted images lived only in the
agent's per-conversation in-memory `visionattach.Store`. The chat placeholder
exposed an `image_id`, but shell/debug tools could not retrieve the original
bytes, so the same image could not easily be sent to two different providers for
an apples-to-apples comparison.

Fixes landed:

- `ExportImage(conversation_id, image_id)` gRPC RPC returns `found`,
  `media_type`, and raw decoded bytes from the live attachment store.
- `agentclient.ExportImage(ctx, conversationID, imageID)` wrapper added.
- `conversation_id` may now be empty; the server searches live attachment stores
  by `image_id` and succeeds only if exactly one live image matches.
- Missing images return `found=false` rather than an error; ambiguity returns a
  `FailedPrecondition` asking for an explicit conversation ID.

Live verification after rebuild:

```text
ExportImage("", "img_2db409_3") => found=true media_type="image/png" bytes=5205794
wrote /tmp/img_2db409_3.png
/tmp/img_2db409_3.png: PNG image data, 2784 x 1888
```

This fixes the debugging workflow: the visible placeholder ID is enough to save a
pasted image to `/tmp` and send identical bytes to cloud and local vision models.

### Qwen2.5-VL 3B finding

Candidate:

```text
ggml-org/Qwen2.5-VL-3B-Instruct-GGUF
Qwen2.5-VL-3B-Instruct-Q4_K_M.gguf
mmproj-Qwen2.5-VL-3B-Instruct-Q8_0.gguf
```

Result: **broken in this environment**.

Evidence:

- Managed llama-server was launched with `--mmproj` and `--jinja`.
- Direct `/v1/chat/completions` image requests returned long `@@@@...` output.
- Direct text-only prompts to the same server also returned `@@@@...` output.
- Direct `llama-cli` text-only against the same GGUF hung for minutes.

Conclusion: the failure is below Cercano's image/tool path. It is a bad
Qwen2.5-VL GGUF / llama.cpp build combination on this machine, not an adapter or
projector wiring problem. Catalog entry is retained with `status: broken` and
must not be used as a profile default.

### Moondream2 finding

Candidate:

```text
ggml-org/moondream2-20250414-GGUF
moondream2-text-model-f16_ct-vicuna.gguf
moondream2-mmproj-f16-20250414.gguf
```

Result: **not usable for Cercano's OpenAI-compatible vision path**.

Evidence:

- Model loads and generates coherent English text, so it is not `@`-broken.
- llama-server reports vision enabled with `--mmproj`.
- Image requests through `/v1/chat/completions` fail with:

```text
Failed to tokenize prompt
```

- `/props` reports `chat_format: None`.
- Raw `/completion` with the media marker works mechanically, but hallucinated a
  trivial synthetic image (described a white box with a red bar as a person's
  face).

Conclusion: Moondream would require a special non-OpenAI endpoint path and still
showed poor accuracy. Catalog entry is marked `status: broken`.

### Gemma 3 4B finding

Candidate:

```text
ggml-org/gemma-3-4b-it-GGUF
gemma-3-4b-it-Q4_K_M.gguf
mmproj-model-f16.gguf
```

Runtime and size:

```text
main GGUF: 2.32 GB
projector: 0.79 GB
total disk: ~3.1 GB
measured macOS physical footprint after image inference: ~1.6-1.7 GB
```

Smoke tests:

- `llama-cli` text prompt produced the expected `hello world` response.
- llama-server with `--mmproj --jinja` accepted image requests and correctly
  described a synthetic white rectangle with a red horizontal bar and black
  border.

Quality on a dense Lunie UI screenshot:

- Broad scene understanding: acceptable; identified a colony/base-building game
  UI with status panel, objectives, central viewport, tutorial prompt, and build
  toolbar.
- Exact UI OCR: poor. It missed or corrupted top status bar text, toolbar labels,
  hotkeys, and costs; e.g. misread `Autofactory` as `Nutfactory`/similar.
- Task understanding: mostly correct at a high level, but less precise than
  cloud.

Conclusion: Gemma 3 4B is a valid lightweight local fallback, but not good enough
to replace cloud vision for dense development screenshots or exact UI text
extraction.

### Gemma 3 12B finding

Candidate:

```text
ggml-org/gemma-3-12b-it-GGUF
gemma-3-12b-it-Q4_K_M.gguf
mmproj-model-f16.gguf
```

Downloaded files:

```text
~/.cercano/models/gemma-3-12b-it-q4_k_m/gemma-3-12b-it-Q4_K_M.gguf   6.8G
~/.cercano/models/gemma-3-12b-it-q4_k_m/mmproj-model-f16.gguf        815M
total disk: ~7.6G
```

Launch command used:

```bash
/opt/homebrew/bin/llama-server \
  --model gemma-3-12b-it-Q4_K_M.gguf \
  --mmproj mmproj-model-f16.gguf \
  --host 127.0.0.1 \
  --port 58995 \
  --ctx-size 8192 \
  --gpu-layers auto \
  --jinja
```

Runtime:

```text
healthy after ~7s
modalities: {'vision': True, 'video': True, 'audio': False}
```

Measured memory after one text request and three image requests:

```text
phys_footprint:      3630 MB
phys_footprint_peak: 3763 MB
```

Quality on the same exported Lunie screenshot (`/tmp/img_2db409_3.png`,
2784x1888):

- Text smoke test: `Say exactly: hello world` => `hello world`.
- Broad scene understanding: substantially better than 4B. It correctly saw a
  base/colony-building game UI with resource/status panel, objectives, central
  isometric viewport, tutorial prompt, and bottom building toolbar.
- Still made high-level mistakes: misidentified the game as `Autonauts` instead
  of Lunie and hallucinated/misnamed some toolbar items.
- OCR was much better than 4B but still below cloud. It read many fields
  correctly:

```text
Pop: 4
O2: 50.0kg -3.4/d
Water: 150.0kg -12.0/d
Stock: 32 Fe | 10 IC | 0 Ore
Time: day 0
Speed: paused
Capacity: 4
Survive 7 days
Produce 20 kg O2
Delivery Rover #1: Idle
Click a tile adjacent to your Hab or Solar Array to queue the Greenhouse.
Skip tutorial
Hab / Solar / Garden / Water / Mine / Conduit
```

But it still made OCR errors relative to cloud:

```text
Food: 280.0kg -0.0/d   # cloud read -8.0/d
Power: 7/39 kW         # cloud read 7/30 kW
Autofactory            # misread as Nutrafactory
Reclaim                # misread as Refinery
LS [L]                 # missed
hotkeys/costs          # mostly missed or unreliable
```

Task understanding was mostly correct: it understood the user should click a tile
adjacent to the Hab or Solar Array to queue a Greenhouse. It was less precise
than cloud about the UI mapping: the actual selected/relevant build option is
`Garden [G]`, which queues the Greenhouse.

Conclusion: Gemma 3 12B is the best local candidate tested so far. It is a clear
upgrade over 4B at only ~3.7 GB physical footprint, but it is still not
cloud-quality for dense UI screenshots and exact text extraction.

### Cloud baseline finding

The live cloud baseline during this pass was `openai-responses:gpt-5.5` (not
Opus in that session). On the same screenshot, cloud was clearly superior:

- Correctly identified the Lunie UI and its regions.
- Extracted dense status bar, objective, tutorial, and toolbar text much more
  accurately.
- Correctly understood the action: place/queue a Greenhouse by selecting/using
  `Garden [G]` on a tile adjacent to the Hab or Solar Array.

### Recommendation after testing

Current best policy:

- Keep image inspection **cloud-first** whenever cloud is allowed.
- Use local Gemma as fallback for `open_only` / offline cases.
- Prefer **Gemma 3 12B** over 4B as the local vision default if the extra RAM is
  acceptable: ~3.7 GB physical footprint versus ~1.7 GB for 4B, with much better
  OCR and task understanding.
- Do not route dense development screenshots to local vision by default yet;
  exact UI reading still needs cloud quality.

Candidate status summary:

| Model | Status | Notes |
|---|---|---|
| Qwen2.5-VL 3B Q4_K_M | broken | `@` spam / hang even text-only |
| Moondream2 F16 | broken | OAI chat vision tokenization fails; raw path hallucinated |
| Gemma 3 4B Q4_K_M | usable fallback | tiny RAM, broad understanding, poor UI OCR |
| Gemma 3 12B Q4_K_M | best local candidate | ~3.7 GB RAM, much better than 4B, still below cloud |

