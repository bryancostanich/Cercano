# Vision (Image Input) — Provider-Layer Plumbing — Design

**Status:** Design approved 2026-06-27. Not yet implemented. Built together with
[cloud-openai.md](./cloud-openai.md) (the OpenAI provider's adapter consumes the
image block).

Add image content to the `llm` message model and teach every provider adapter to
translate it, so the provider layer is vision-ready and consistent. This is layers
1–2 of image support; the **inbound path** (CLI attach + proto image field — how
images actually enter the agent) is deferred to the broader "tackle images"
initiative.

## 1. Image block (the foundation)

Extend `internal/llm/messages.go` `Block`:

```go
const BlockImage BlockType = "image"

// added to Block:
MediaType string `json:"media_type,omitempty"` // e.g. "image/png", "image/jpeg"
ImageData string `json:"image_data,omitempty"` // base64-encoded image bytes
```

**Base64 is the canonical (and only) representation** — not URLs. Ollama cannot
fetch a URL; it requires raw image bytes. Base64 (media type + data) is the one
form that translates to all three backends, so every adapter stays on a single
path. URL support can be added later if a need appears; it is explicitly out of
scope here.

An image rides inside a normal message: a user `llm.Message` may carry
`Blocks: [BlockText, BlockImage]` (text + one or more images).

## 2. Adapter translation

Each provider adapter gains image handling and flips
`Capabilities().SupportsVision` to `true`.

- **Anthropic** (`internal/llm/anthropic/adapter.go`) — `BlockImage` →
  `{type:"image", source:{type:"base64", media_type:MediaType, data:ImageData}}`
  (the SDK's base64 image source param).
- **OpenAI** (`internal/llm/openai/adapter.go`, new in the sibling sub-project) —
  the user message becomes multi-part content: a text part plus an `image_url`
  part whose URL is a data URI `data:<MediaType>;base64,<ImageData>`.
- **Ollama** (`internal/llm/ollama/adapter.go`) — append the decoded bytes
  (`base64.StdEncoding.DecodeString(ImageData)`) to the message's `Images` field;
  text stays in `Content`.

A message with mixed text + image blocks must produce the provider's correct
multi-part user message (text and image together), not two separate messages.

## 3. Capabilities

`anthropic`, `ollama`, and `openai` set `SupportsVision: true`. (Nothing in the
tool-loop branches on it yet; it becomes meaningful when the inbound path lands,
and lets a future router avoid sending images to a non-vision backend.)

## 4. Tool-loop / message flow

No tool-loop change expected — it passes `llm.Block`s through opaquely. The image
block flows through `ConvHistory` / message construction like any other block.
(Confirm during implementation that no block-type switch silently drops unknown
types; if one does, add the `BlockImage` case.)

## Testing

- **messages**: `BlockImage` round-trips through any JSON marshal/unmarshal in the
  `llm` package.
- **anthropic adapter**: a user message with text + image → SDK params containing
  the base64 image source with the right media type; `SupportsVision` true.
- **ollama adapter**: text + image → `Content` plus `Images` with the decoded
  bytes; `SupportsVision` true.
- **openai adapter**: covered in the OpenAI sub-project — text + image → multi-part
  content with the `data:` URI; `SupportsVision` true.
- Decode-error handling: a `BlockImage` with non-base64 `ImageData` surfaces a
  clear error rather than sending garbage (at least for Ollama, which decodes).

## Out of scope

- **Inbound path** — CLI/clients attaching images, and a proto/request image
  field. Nothing produces image blocks yet; this plumbing is exercised only by
  adapter tests until that lands.
- **Image URLs** (base64 only, per §1).
- Image *outputs* / generation; document/PDF inputs.
