# Image Input (drop / paste images into the CLI) — Design

**Status:** Designed 2026-06-29.

Let a user attach images to a prompt in `cercano-cli` by **dropping an image file
onto the terminal** or **pasting a copied image (Cmd+V)**. The attachment shows in
the prompt as an atomic `[image N]` chip, and the image travels with the turn to
the model, interleaved at the chip's position.

## The mechanism (why this is mostly a paste problem)

Terminals have no drag-and-drop-with-bytes API. Dragging an image file onto the
terminal makes the emulator **paste the file's path** as text (often quoted or
with backslash-escaped spaces; multiple files arrive space/newline-separated).
Pasting a *copied* image (Cmd+V) puts raw bytes on the OS clipboard and typically
emits **no usable text** (some terminals emit nothing at all). So capture is two
paths: detect image-file paths in pasted text, and peek the OS clipboard for image
bytes.

The LLM wire layer already supports images end-to-end — `llm.Block{Type:
BlockImage, MediaType, ImageData(base64), ImageURL}` (`internal/llm/messages.go`),
with both the Anthropic and OpenAI adapters serializing image blocks. The only gap
is the **user-request path**: `ProcessRequestRequest.input` is a plain string and
the server builds a single `BlockText` (`toolloop.go`, `llm_adapter.go`). This
feature closes that gap and adds the CLI capture + chip UX.

## 1. Capture (CLI)

### 1a. File drop (paste of path(s))

On a `tea.PasteMsg`, before inserting, classify the content:
- Tokenize into candidate paths, handling: a bare path, single/double-quoted
  paths, backslash-escaped spaces, and multiple paths separated by whitespace/
  newlines.
- `os.Stat` each candidate; classify as an image by extension (`.png`, `.jpg`,
  `.jpeg`, `.gif`, `.webp`) confirmed with a content sniff (`http.DetectContentType`
  on the first 512 bytes).
- **If the whole paste resolves to one or more existing image files**, treat it as
  a drop: read each file (subject to the size cap below) and attach it. Otherwise
  insert the content as literal text (unchanged behavior).

A mixed paste (part path, part prose) is treated as literal text — keeping the
heuristic conservative avoids hijacking a path someone meant to type.

### 1b. Clipboard image (Cmd+V)

Behind a small platform interface:

```go
// clipboardImage returns a decoded image from the OS clipboard, if present.
func clipboardImage() (data []byte, mediaType string, ok bool)
```

- **macOS first** (`clipboard_darwin.go`): export the pasteboard image to bytes.
  Leading approach: an `osascript` snippet that writes the clipboard PNG to a temp
  file we then read; fall back to `pngpaste` if it's on `PATH`. **This bytes-out-of-
  pasteboard extraction is the one spike** — validate the exact mechanism early in
  implementation before building the UX on top.
- Other platforms (`clipboard_other.go`): return `ok=false` (no clipboard-image
  support yet; file drop still works).

Trigger: on a `PasteMsg` whose text is empty/whitespace, peek the clipboard; plus a
dedicated key fallback (e.g. `ctrl+v`) that forces a clipboard-image check, for
terminals that emit no event on image paste. If the clipboard holds an image,
attach it; otherwise fall through to normal paste handling.

## 2. The `[image N]` chip (prompt_input.go)

`promptInput` (custom widget, `value []rune` + cursor/selection/undo) gains:

- An **attachment registry**: `attachments []promptImage{ id int; data []byte;
  mediaType string; source string }`, `id` assigned sequentially per prompt
  (`[image 1]`, `[image 2]`, …).
- **Atom spans**: a list of `{start, end, id}` rune ranges marking each chip's
  `[image N]` text as atomic. Ranges are maintained on every edit (shift on
  insert/delete after the edit point).

Behavior:
- **Insert**: the literal runes `[image N]` are inserted at the cursor; a span +
  attachment are registered.
- **Render** (`View`): the chip text is styled (accent) so it reads as a token.
- **Backspace/Delete** adjacent to or inside a span removes the **entire span** and
  drops its attachment from the registry.
- **Cursor left/right** jumps over a span (treats it as one position); **selection**
  that intersects a span includes the whole span.
- **Undo/redo** snapshots extend to capture spans + attachments alongside the
  existing text/cursor/selection snapshot.
- `SetValue("")` (post-submit) clears attachments and spans.

## 3. Transport & server

- **Proto** (`agent.proto`): add to `ProcessRequestRequest`:
  ```proto
  message InlineImage { int32 index = 1; bytes data = 2; string media_type = 3; }
  // in ProcessRequestRequest:
  repeated InlineImage images = 9;
  ```
  `input` keeps the `[image N]` markers so the server can place each image.
- **agentclient**: `StreamChat` / `mainAgentDriver.Submit` take an
  `images []InlineImage` parameter alongside `input`.
- **Server**: a helper
  `buildUserBlocks(input string, images []InlineImage) []llm.Block` splits `input`
  on the `[image N]` markers and interleaves `BlockText` runs with
  `BlockImage{MediaType, ImageData: base64(img.data)}` in marker order. Wire it into
  both user-message construction sites (`toolloop.go`, `llm_adapter.go`). A turn
  with no images produces a single `BlockText` exactly as today.

```
look at  [image 1]  and compare to  [image 2]
  → [ text "look at " | image#1 | text " and compare to " | image#2 ]
```

## 4. Vision capability — warn, but allow (interim)

`GetProviderCapabilities()` already reports `SupportsVision`
(`agentclient.ProviderCaps.SupportsVision`). The CLI caches it and refreshes when
the model / cloud profile / locus mode changes. If an image is attached while the
active model isn't vision-capable, show a subtle dim notice near the prompt (e.g.
`⚠ active model can't see images`) — the send is still allowed.

**Future direction (not in this feature):** once capability-aware routing exists,
an image turn on a non-vision active model should auto-route to a vision-capable
model. The warning here is the interim behavior until that lands; keep it minimal
so it's cheap to retire. The design must not preclude later routing (nothing here
blocks the image at the client based on capability).

## 5. Limits & errors

- Accept `png`, `jpeg/jpg`, `gif`, `webp`; media type from extension confirmed by
  content sniff (or sniff alone for clipboard bytes).
- Per-image cap **20 MiB** (mirror server `maxImageBytes`). An oversized file is
  rejected with a brief inline error and is **not** attached.
- A non-image or non-existent path paste stays literal text.
- Clipboard peek failure / unsupported platform: silently fall through to normal
  paste (no error noise).

## 6. Components touched

| Area | Files |
|---|---|
| Capture + chip | `internal/ui/prompt_input.go`, `internal/ui/model.go` (paste/key routing) |
| Clipboard | new `internal/ui/clipboard_darwin.go`, `internal/ui/clipboard_other.go` |
| Vision notice | `internal/ui/model.go` (caps cache + prompt notice) |
| Send | `internal/ui/main_agent_driver.go`, `pkg/agentclient/client.go` |
| Proto + server | `proto/agent.proto`, server request mapping + `buildUserBlocks` (`internal/server/...`) |

## 7. Testing

- **Path classification** (pure): quoted, escaped-space, multi-path, image vs
  non-image, non-existent — drop vs literal-text decision.
- **Chip atomicity** (prompt_input): insert chip; backspace deletes whole chip +
  drops attachment; cursor skip; selection swallow; undo/redo restores chip +
  attachment; multiple chips; `SetValue("")` clears.
- **buildUserBlocks** (server): markers split and interleave in order; no images →
  single text block; image at start/middle/end; unknown/again markers handled.
- **Proto round-trip**: `InlineImage` bytes + media type survive.
- **Vision notice**: notice shown when `SupportsVision=false` and an image is
  attached; absent otherwise.
- **Limits**: >20 MiB rejected, not attached; unsupported type stays literal.
- Clipboard extraction is validated by the implementation spike + a macOS-gated
  smoke test (skipped where the platform/clipboard tool is unavailable).

## 8. Out of scope

- Capability-aware routing (the future direction in §4) — separate effort.
- Non-image file attachments (drop a PDF / text file) — this feature is images only.
- Linux/Windows clipboard-image reading — file drop works everywhere; clipboard
  image is macOS-first, others stubbed.
- Image thumbnails / inline preview rendering in the terminal — the chip is text.
- SSRF hardening of `ResolveImageBytes`' URL path — unrelated; this feature sends
  bytes, not URLs.
