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
