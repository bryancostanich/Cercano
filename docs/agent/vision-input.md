# Vision (Image Input) — Provider-Layer Plumbing — Design

**Status:** Provider-layer plumbing implemented 2026-06-27. Inbound path still deferred. Built together with
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

// added to Block (exactly one of ImageData / ImageURL is set):
MediaType string `json:"media_type,omitempty"` // e.g. "image/png" (required for base64; optional hint for URL)
ImageData string `json:"image_data,omitempty"` // base64-encoded image bytes
ImageURL  string `json:"image_url,omitempty"`  // http(s) image URL
```

**Both forms are supported** — base64 bytes *and* an http(s) URL — because the
cloud providers accept either natively. **Exactly one** of `ImageData` / `ImageURL`
is set per block. The asymmetry to design around: **Ollama (local) takes raw bytes
only — no URLs** — so a URL image bound for Ollama is fetched server-side and
base64-encoded before sending (see §2).

An image rides inside a normal message: a user `llm.Message` may carry
`Blocks: [BlockText, BlockImage]` (text + one or more images).

## 2. Adapter translation

Each provider adapter gains image handling and flips
`Capabilities().SupportsVision` to `true`.

- **Anthropic** (`internal/llm/anthropic/adapter.go`) — both native:
  `ImageData` → `{type:"image", source:{type:"base64", media_type, data}}`;
  `ImageURL` → `{type:"image", source:{type:"url", url}}`.
- **OpenAI** (`internal/llm/openai/adapter.go`, new in the sibling sub-project) —
  multi-part content with an `image_url` part: `ImageURL` → the URL directly;
  `ImageData` → a data URI `data:<MediaType>;base64,<ImageData>`.
- **Ollama** (`internal/llm/ollama/adapter.go`) — bytes only. `ImageData` →
  `base64.StdEncoding.DecodeString` → message `Images`. `ImageURL` → **fetch it
  server-side** (HTTP GET, with a context timeout and a sane size cap) → bytes →
  `Images`. A shared helper (e.g. `llm.ResolveImageBytes(ctx, block)`) does the
  base64-decode-or-fetch so the logic isn't Ollama-specific.

A message with mixed text + image blocks must produce the provider's correct
multi-part user message (text and image together), not two separate messages.

**Note on URL fetching:** the Ollama fetch issues an outbound GET to a
caller-supplied URL. With no inbound path yet, no untrusted URL reaches it, but
when the inbound path lands, that fetch is an SSRF surface — bound it (timeout,
max size, and ideally an http(s)-only / no-internal-address check) at that time.

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
- **anthropic adapter**: text + base64 image → base64 source; text + URL image →
  url source; right media type; `SupportsVision` true.
- **ollama adapter**: base64 image → `Images` with decoded bytes; URL image →
  fetched (against an `httptest` server) → `Images`; `SupportsVision` true.
- **`ResolveImageBytes`**: base64 path decodes; URL path GETs (mock server);
  non-base64 `ImageData` and a failed/oversized fetch surface clear errors.
- **openai adapter**: covered in the OpenAI sub-project — text + image (URL → URL
  part; base64 → `data:` URI part); `SupportsVision` true.

## Out of scope

- **Inbound path** — CLI/clients attaching images, and a proto/request image
  field. Nothing produces image blocks yet; this plumbing is exercised only by
  adapter tests until that lands. (The full SSRF hardening of the URL fetch also
  lands with the inbound path — see §2 note.)
- Image *outputs* / generation; document/PDF inputs.
