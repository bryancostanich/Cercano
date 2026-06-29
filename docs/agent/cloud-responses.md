# OpenAI Responses API Provider — Design

**Status:** Designed 2026-06-29. Sub-project 3 of the multi-cloud effort
(foundation = [cloud-profiles.md](./cloud-profiles.md); SP2 = [cloud-openai.md](./cloud-openai.md)).

Add an `llm.Provider` that speaks the OpenAI **Responses API** (`POST /v1/responses`),
wired in via the `responses` flavor the foundation reserved. The draw is twofold:
forward-compatible parity (OpenAI is steering new models/features to Responses as
its going-forward default) and **reasoning-model quality** — Responses lets a
reasoning model keep its internal reasoning across tool-call turns, which Chat
Completions discards each turn.

## Core decisions

- **Hand-rolled client.** `go-openai` (our Chat Completions library) has **no**
  Responses API support — no `CreateResponse`, no Responses request/response/stream
  types. Rather than add a second large OpenAI SDK, we hand-roll a focused client
  for exactly what we need (text, tools, vision, streaming, reasoning round-trip).
  This is the "hand-roll when we need it" path SP2 deliberately reserved. Isolated
  behind `llm.Provider`; nothing upstream knows the wire shape.
- **Stateless, `store=false`.** The provider stays a pure function of its inputs,
  like every other backend — each turn sends the full conversation. We do **not**
  use `previous_response_id`/server-side state. Reasoning is preserved by carrying
  the model's reasoning items in **our own** conversation history and handing them
  back each turn. `store=false` means OpenAI retains nothing (fits local-first).
- **Reasoning quality is unaffected by going stateless.** The model behaves
  identically whether OpenAI holds the reasoning (`previous_response_id`) or we
  resend it — what matters is that the prior reasoning items are present in the
  input. With `store=false` OpenAI returns the reasoning as an **opaque encrypted
  blob** (requested via `include:["reasoning.encrypted_content"]`); we store it and
  hand it back verbatim, the model decrypts it server-side. We never read it.
- **OpenAI-native, not a compat fanout.** Unlike the `chat_completions` provider
  (which fans out to many compat endpoints), Responses is effectively OpenAI-only
  today. So this provider does **not** carry the `backend`/quirks layer. A
  `base_url` override is still supported (Azure / a local proxy). Responses error
  bodies are object-shaped, so the array-error normalization from
  [per-backend-quirks.md](./per-backend-quirks.md) is unnecessary; only the
  transient-retry behavior is reused (see §6).

## 1. Package structure

`internal/llm/responses/` (mirrors `internal/llm/openai/`):
- `client.go` — `Config{BaseURL, APIKey, Model}`; `NewClient(Config) *Client`;
  `Name() → "openai-responses"`; `Capabilities()`; `Chat(ctx, llm.ChatRequest)
  (llm.ChatResponse, error)`; `StreamChat(ctx, llm.ChatRequest) (llm.StreamReader,
  error)`. Builds requests against `BaseURL` (empty → `https://api.openai.com/v1`),
  endpoint path `/responses`.
- `adapter.go` — pure translation, `llm` ↔ Responses wire types.
- `stream.go` — a `streamReader` parsing the Responses SSE stream into
  `llm.StreamEvent`s.
- `wire.go` — the hand-rolled Go structs for the Responses request, response, and
  streaming events (only the fields we use).

The provider implements the existing `llm.Provider` interface verbatim
(`provider.go:30`) — `Name`/`Capabilities`/`Chat`/`StreamChat` — so the tool-loop,
router, and coordinator consume it identically to anthropic/openai/ollama.

## 2. Message-model change: a reasoning block

Extend `internal/llm/messages.go` `Block` so the model's reasoning round-trips
through `ConvHistory` like any other turn content:

```go
const BlockReasoning BlockType = "reasoning"

// added to Block (set only for BlockReasoning):
ReasoningID   string `json:"reasoning_id,omitempty"`   // the Responses reasoning item id
ReasoningData string `json:"reasoning_data,omitempty"` // opaque encrypted_content blob (we never read it)
```

- **Opaque + provider-specific.** We never inspect `ReasoningData`; we store it and
  send it back. It is meaningful only to the Responses backend.
- **Safe across providers.** The anthropic and openai adapters switch on block type
  and ignore unknown types (no `default`-case error), so a `BlockReasoning` routed
  to a non-Responses backend is harmlessly dropped. Implementation MUST verify this
  (grep each adapter's block switch) — same check the vision plan called for. The
  tool-loop passes blocks through opaquely and needs no change.
- **No human-readable summary stored** (YAGNI). We carry only the encrypted blob +
  id needed for round-trip; a visible reasoning *summary* can be added later if a UI
  wants to display it.

## 3. Translation (`adapter.go`)

The project's message model is block-based (`llm.Message{Role, Blocks[]}`). The
Responses API takes a flat `input[]` array of typed items plus a top-level
`instructions` string.

**Outbound — `llm.ChatRequest` → Responses request:**
- `ChatRequest.System` → top-level `instructions`.
- `ChatRequest.Messages` → `input[]` items, preserving order:
  - `BlockText` (user/assistant) → a `message` item with `content:[{type:"input_text",text}]`
    (role from the message).
  - `BlockImage` → an `input_image` content part: `ImageURL` passed through, or
    `ImageData` as a `data:<MediaType>;base64,<…>` URI (same rule as the chat adapter).
  - `BlockToolUse{ToolUseID, ToolName, ToolInput}` → a `function_call` item
    `{type:"function_call", call_id:ToolUseID, name:ToolName, arguments:string(ToolInput)}`.
  - `BlockToolResult{ToolUseRef, Content}` → a `function_call_output` item
    `{type:"function_call_output", call_id:ToolUseRef, output:Content}`.
  - `BlockReasoning{ReasoningID, ReasoningData}` → a `reasoning` item
    `{type:"reasoning", id:ReasoningID, encrypted_content:ReasoningData}`.
- `ChatRequest.Tools` → `tools[]` of `{type:"function", name, description, parameters}`
  (flat shape — Responses function tools are not nested under a `function` key the
  way Chat Completions tools are). `ChatRequest.ToolChoice` → `tool_choice`.
- `ChatRequest.MaxTokens` → `max_output_tokens` (when > 0). `Temperature` → `temperature`.
- Always set `store:false` and `include:["reasoning.encrypted_content"]`. Set
  `model` from `req.Model` or the client default. Set `stream:true` for `StreamChat`.

**Inbound — Responses response → `[]llm.Block`:** walk `output[]`:
- `message` item → its `output_text` content → `BlockText`.
- `function_call` item → `BlockToolUse{ToolUseID:call_id, ToolName:name, ToolInput:arguments}`.
- `reasoning` item → `BlockReasoning{ReasoningID:id, ReasoningData:encrypted_content}`.
- Map `response.usage` (`input_tokens`/`output_tokens`) → `ChatResponse` token
  counts; the response `status`/incomplete reason → `StopReason`.

## 4. Streaming (`stream.go`)

The Responses API streams typed Server-Sent Events (`event:`/`data:` lines, each
`data:` a JSON object with a `type`). Map to the existing `StreamEventType`
vocabulary (`stream.go:8-14`) so the tool-loop is provider-agnostic:

| Responses SSE event | Emitted `llm.StreamEvent` |
|---|---|
| `response.created` | `EventMessageStart` |
| `response.output_text.delta` | `EventTextDelta` (TextDelta = delta) |
| `response.output_item.added` (function_call) | `EventToolUseStart` (ToolUseID=call_id, ToolName=name) |
| `response.function_call_arguments.delta` | `EventToolUseInputDelta` (TextDelta = fragment) |
| `response.output_item.done` (function_call) | `EventToolUseStop` |
| `response.completed` | `EventMessageStop` (+ usage from the final response) |
| `response.error` / `error` | `EventError` |

- **Reasoning while streaming (decided — dedicated event):** the encrypted reasoning
  arrives on the reasoning item's `response.output_item.done` (it is not a token
  delta). Add a new stream event `EventReasoning` to the vocabulary
  (`stream.go`), carrying `ReasoningID` + `ReasoningData` (reuse the existing
  `StreamEvent.ToolUseID`/a new field, but keep it explicit). The Responses reader
  emits it when a reasoning item completes; `collectStream` (`toolloop.go:343`)
  gains one case that flushes any in-progress text/tool block and appends a
  `BlockReasoning` — so the streamed block order matches the non-streaming adapter
  (reasoning *before* the `function_call`), multiple reasoning items are each
  preserved, and the anthropic/openai readers (which never emit `EventReasoning`)
  are unaffected. This makes reasoning a first-class stream event that a future
  Anthropic extended-thinking path could reuse.
- Tool-call arguments stream as fragments and are reassembled by the existing
  tool-loop exactly as for the other providers (START → INPUT_DELTA… → STOP).
- Usage: `response.completed` carries the final `usage`; emit both token counts on
  `EventMessageStop` (mirrors the OpenAI chat reader's end-of-stream usage).

## 5. Capabilities + factory

`Capabilities{SupportsTools:true, SupportsParallelTools:true, SupportsVision:true,
SupportsCaching:false}` (Responses auto-caches server-side; nothing for us to
control).

In `cloudfactory.BuildCloudProvider`, fill the reserved case:

```go
case FlavorResponses:
    return responses.NewClient(responses.Config{BaseURL: p.BaseURL, APIKey: apiKey, Model: p.Model}), nil
```

A user reaches it with a `responses`-flavor profile:

```yaml
cloud_profiles:
  - name: openai-responses
    flavor: responses
    model: gpt-5    # or an o-series / reasoning model
```

## 6. Transport, retries, errors

- **Retry (decided — shared `internal/llm/httpx`).** Reuse transient-failure retry
  (429/500/502/503, exponential backoff) so Responses gets the same resilience as
  the chat provider. The chat retry currently lives in `internal/llm/openai`'s
  `normalizingDoer` (unexported), entangled with array-error normalization. Extract
  the retry round-tripper into a new neutral `internal/llm/httpx` package (a plain
  `Do`/`http.RoundTripper` wrapper parameterized by a `RetryPolicy`), and have BOTH
  providers depend on it rather than on each other: the Responses client uses it
  directly; `openai`'s `normalizingDoer` is refactored to **compose**
  `httpx.RetryTransport` + keep its own error normalization. The existing
  `internal/llm/openai/transport_test.go` (six tests) guards that refactor. The
  Responses client needs **only** retry — not array-error normalization (that is
  Chat-Completions-error-shape specific).
- **Errors.** Responses returns object-shaped errors (`{"error":{...}}`). The
  hand-rolled client decodes that into a Go error carrying status + message — no
  array normalization needed. A non-2xx with an unparseable body yields a generic
  "responses: status N" error.
- **Image URL fetching.** Like OpenAI-native chat, the Responses backend fetches
  `input_image` URLs server-side, so no base64 pre-resolution is required here
  (that was a compat-backend quirk). Base64 images are sent inline as today.

## 7. Testing

- **adapter** round-trips (table tests, no network): system→instructions; user text;
  text+image (URL and base64 → `input_image`); single and parallel `function_call`;
  `function_call_output`; a `reasoning` block → reasoning input item and back;
  tools → flat function tools; `store:false` + `include` always set.
- **stream** against canned SSE fixtures (a `strings.Reader` of recorded
  `event:`/`data:` lines, no network): text deltas accumulate; fragmented
  `function_call_arguments` reassemble into one `ToolInputRaw`; reasoning
  `encrypted_content` surfaces on stop; usage from `response.completed`; an error
  event → `EventError`.
- **client** against an `httptest` server returning a recorded Responses JSON body:
  one non-stream tool round-trip and one streamed round-trip; a 500 retried then
  succeeding (via the shared retry transport).
- **factory**: `responses` flavor → a `*responses.Client` with `Name()=="openai-responses"`
  and the expected capabilities.
- **messages**: `BlockReasoning` round-trips through JSON marshal/unmarshal; confirm
  anthropic + openai adapters drop it without error.
- **integration (gated, `INTEGRATION_TEST=1`):** a live `Chat`, a streamed `Chat`,
  a tool call, and a reasoning-model round-trip (two turns with a tool call in
  between) asserting the reasoning block is carried back — mirroring the gated
  OpenAI chat tests.

## Out of scope

- **`previous_response_id` / server-side conversation state** — deliberately not
  used (stateless, `store=false`). A seam could be added later if a real need
  appears; not built now.
- **Hosted built-in tools** — web search, file search, code interpreter. These are
  a distinct, larger surface (how they interleave with our MCP/local tool-loop is
  its own design); deferred.
- **Compat-endpoint fanout / the `backend` quirks layer** — Responses is treated as
  OpenAI-native (plus a `base_url` override for Azure/proxy).
- **Bedrock** (SP4) — separate flavor.
- **Displaying reasoning** — we carry the encrypted blob for round-trip only; no
  human-readable reasoning summary is stored or surfaced yet.
