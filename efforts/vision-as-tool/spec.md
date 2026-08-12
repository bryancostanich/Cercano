# Vision as a tool

## Goal

Keep the primary local reasoning model (for example GLM-4.5-Air) in charge of
planning, tool use, and final answers, while giving it access to a separate
vision-capable model as callable "eyes".

Instead of making a huge vision-language model the default for all local work,
image-bearing turns become text/tool turns:

```text
User attaches image(s)
        ↓
Cercano stores images in a per-conversation attachment store
        ↓
Reasoning model receives explicit image placeholders, not raw image blocks
        ↓
Reasoning model may call inspect_image(image_id, question)
        ↓
Vision model answers the focused visual question
        ↓
Reasoning model continues and produces final answer
```

This lets GLM-4.5-Air remain the default local text/reasoning model while a
smaller or separate vision model handles only the visual inspection work.

## Context

The preceding `local-model-vision` effort established the required plumbing:

- text-only local models now report truthful `SupportsVision:false`;
- image blocks are stripped before no-vision backends can 500;
- curated llama-server models can declare `supports_vision` and `mmproj_file`;
- `ModelRecord` carries `SupportsVision` and `MmprojPath`;
- llama-server launches vision models with `--mmproj <path>`;
- GLM-4.5V was added as a heavy GLM vision entry and downloaded;
- GLM-4.5-Air was confirmed text-only and restored as the default local model.

The conclusion from that work: GLM-4.5V proves the plumbing but is not the ideal
default. The right architecture is a separate vision tier/tool that works with
the text reasoning model.

## Settled design decisions

### D1 — Image identity lives in a separate attachment store

Do **not** overload `llm.Block` with provider-irrelevant IDs, and do **not** rely
on fragile per-turn ordinal conventions alone.

Create a per-conversation in-memory image attachment store keyed by generated
image IDs. The store maps IDs to image bytes, media type, hash, approximate size,
and lifecycle metadata needed by tools.

Initial scope:

- in-memory only;
- per live conversation/session;
- not persisted to SQLite or disk in V1;
- graceful stale-reference behavior after restart/resume.

Generated IDs must be conversation-unique. Prefer a short content-hash + ordinal
shape, for example:

```text
img_7f3a9c_1
```

### D2 — Reasoning model sees placeholders, not raw images

Before sending the user turn to the reasoning model, replace raw image blocks
with explicit placeholders:

```text
[Image img_7f3a9c_1 attached for this live conversation. Use inspect_image(image_id="img_7f3a9c_1", question=...) to ask focused visual questions. If unavailable, ask the user to reattach the image.]
```

The reasoning model should not receive raw image blocks in this path. That keeps
GLM-4.5-Air / other text-only local models safe and makes the tool affordance
obvious.

### D3 — Vision model is invoked through a real built-in tool

Add a built-in tool:

```json
{
  "name": "inspect_image",
  "description": "Ask the configured vision model a focused question about an attached image.",
  "input_schema": {
    "type": "object",
    "properties": {
      "image_id": {
        "type": "string",
        "description": "The image ID shown in the image placeholder, such as img_7f3a9c_1."
      },
      "question": {
        "type": "string",
        "description": "A focused visual question to answer from the image."
      }
    },
    "required": ["image_id", "question"]
  }
}
```

The tool implementation performs a plain, tool-less one-shot call to a
vision-capable provider/model. The vision model must not receive tools, and it
must not recursively call back into the agent tool loop.

### D4 — Vision model is selected through a first-class `vision` tier

Add a real model tier/profile named `vision` rather than reusing an unrelated
speed/text tier or adding a one-off `vision_model` field.

Example config:

```yaml
models:
  open:
    overrides:
      llama_server:
        everyday: glm-4.5-air-q4_k_m
        vision: gemma-3-4b-it-q4_k_m
```

`inspect_image` resolves the `vision` tier for the active locus/runtime policy.
A future UI can expose this as a normal tier in the model picker/catalog.

### D5 — Tool result is a stable hybrid text envelope

The vision model does **not** need to produce strict JSON. The tool wraps its
answer in a stable, auditable text envelope:

```text
Image img_7f3a9c_1 inspection result:
Question: What error text is visible?
Answer: The screenshot shows "image input is not supported — hint: ... provide the mmproj".
Confidence: high
Source: local vision model gemma-3-4b-it-q4_k_m
```

The required fields are image ID, question, and answer. Confidence/source are
optional but useful.

### D6 — Cache is per-conversation in-memory, with resume/stale guards

Cache successful inspection results per live conversation:

```text
(image_id, normalized_question) -> ImageInspectionResult
```

The cache is not persisted in V1.

Guardrails:

- if cache is cleared but the attachment still exists, the tool re-calls the
  vision model;
- if the attachment is gone (restart/resume, memory eviction, unknown ID), return
  a clear tool result telling the model/user to reattach the image;
- no hallucinated inspection;
- no cross-conversation cache contamination;
- memory caps for max image count and total image bytes per conversation.

### D7 — Fallback is locus-aware

`inspect_image` tries providers in this order:

1. configured local/open `vision` tier;
2. if unavailable or failing **and the current locus mode allows cloud**, a
   cloud vision-capable provider;
3. otherwise, a graceful unavailable result.

Cloud fallback is not forbidden by default, but it must obey the current locus.
If the user is in local/open-only mode, do not call cloud.

Unavailable result example:

```text
Image inspection unavailable:
No local vision model is configured or available for the `vision` tier, and the current locus mode is open-only. Ask the user to configure/download a local vision model or reattach/describe the image.
```

Cloud fallback result should be honest in the tool trace:

```text
Local vision model unavailable; using cloud vision fallback.
Image img_... inspection result:
...
Source: cloud vision provider anthropic
```

## Scope

### In

- Per-conversation in-memory image attachment store.
- Placeholder rewriting for user image blocks before reasoning-model calls.
- Built-in `inspect_image` tool registration and execution.
- First-class `vision` model tier/profile and resolver support.
- One-shot tool-less vision provider call.
- Per-conversation in-memory inspection cache.
- Stale-reference/resume guardrails.
- Locus-aware local→cloud fallback.
- Unit and integration tests around routing, cache, and failures.

### Out for V1

- Persisting image attachments in SQLite or on disk.
- Persistent image-inspection cache across restarts.
- Bounding boxes / visual region references.
- Automatic screenshot OCR preprocessor.
- UI thumbnail gallery.
- Making GLM-4.5V the default local model.

## Acceptance criteria

1. A local text reasoning model with `SupportsVision:false` can receive an
   image-bearing user turn as text placeholders and successfully call
   `inspect_image`.
2. `inspect_image` sends the real image bytes plus the focused question to a
   configured vision model, with no recursive tool exposure.
3. Repeated identical image questions in the same live conversation hit the
   cache and call the vision provider once.
4. Cache cleared + attachment present re-calls the vision provider successfully.
5. Attachment missing / unknown image ID returns a clear "reattach image" tool
   result and does not crash the turn.
6. No local vision model + cloud-allowed locus uses cloud vision fallback.
7. No local vision model + local/open-only locus returns unavailable and does
   not call cloud.
8. Two live conversations cannot read each other's image attachments or cached
   inspection results.
9. `go build ./...`, `go vet ./...`, and `go test ./...` are green in
   `source/server` at every phase boundary.
