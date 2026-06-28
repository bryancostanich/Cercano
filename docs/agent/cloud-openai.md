# OpenAI Chat Completions Provider — Design

**Status:** Implemented 2026-06-27 (foundation-level).

Sub-project 2 of the multi-cloud effort (foundation = [cloud-profiles.md](./cloud-profiles.md)).
Add an `llm.Provider` that speaks the OpenAI **Chat Completions** API, parameterized
by `base_url` so one backend serves OpenAI *and* every OpenAI-compatible endpoint
(Gemini-compat, Groq, Together, Fireworks, OpenRouter, DeepSeek, local
llama.cpp/vLLM/LM Studio). Wired in via the `chat_completions` flavor the
foundation reserved.

## Approach

Mirror the existing provider packages (`internal/llm/anthropic/`, `internal/llm/ollama/`):
each wraps a Go client library + an adapter that translates between the project's
`llm` types and the wire format. For OpenAI we use **`github.com/sashabaranov/go-openai`**
— lenient, first-class `base_url`, and the de-facto library for hitting compat
endpoints (Groq/Together/local docs all use it).

**Library isolation (important):** `go-openai` is referenced *only* inside the new
`internal/llm/openai/` package, behind the `llm.Provider` interface. Nothing in
the factory, wiring, tool-loop, or co-processor path knows which library serves
OpenAI. Swapping to a hand-rolled HTTP/JSON client later is therefore a contained
change to this one package — a deliberate option for if/when compat-endpoint
quirks outgrow the library.

## 1. Package structure

`internal/llm/openai/`:
- `client.go` — `Config{BaseURL, APIKey, Model}`; `NewClient(Config) *Client`;
  `Name() → "openai"`; `Capabilities()`; `Chat(ctx, llm.ChatRequest) (llm.ChatResponse, error)`;
  `StreamChat(ctx, llm.ChatRequest) (llm.StreamReader, error)`. Builds a
  `go-openai` client with `ClientConfig.BaseURL` overridden when `Config.BaseURL != ""`
  (empty → `https://api.openai.com/v1`).
- `adapter.go` — pure translation functions, `llm` ↔ `go-openai`.
- `stream.go` — a `streamReader` wrapping the `go-openai` stream, emitting `llm.StreamEvent`.

## 2. Translation (`adapter.go`)

The project's message model is Anthropic-shaped (`llm.Message{Role, Blocks[]}` with
`BlockText` / `BlockToolUse` / `BlockToolResult`). OpenAI Chat Completions is
flatter, so the adapter maps:

- **System:** `ChatRequest.System` → a leading `ChatCompletionMessage{Role:"system", Content:…}`.
- **Text:** `BlockText` → message `Content`.
- **Assistant tool call:** `BlockToolUse{ToolUseID, ToolName, ToolInput}` → an
  assistant message with `ToolCalls:[{ID, Type:"function", Function:{Name, Arguments}}]`.
- **Tool result:** `BlockToolResult{ToolUseRef, Content, IsError}` → a
  `ChatCompletionMessage{Role:"tool", ToolCallID:ToolUseRef, Content:…}`.
- **Tools:** `llm.Tool{Name, Description, InputSchema}` → `openai.Tool{Type:"function",
  Function:{Name, Description, Parameters}}`. `llm.ToolChoice` → `openai`'s tool-choice.
- **Response → blocks:** a non-stream `ChatCompletionResponse`'s assistant message
  becomes `llm.Block`s — `Content` → `BlockText`; each `ToolCalls[i]` →
  `BlockToolUse{ToolUseID:ID, ToolName:Function.Name, ToolInput:Function.Arguments}`.
  Map `FinishReason` → `ChatResponse.StopReason`; `Usage` → token counts.

## 3. Streaming (`stream.go`)

OpenAI streams `choices[0].delta`. Two wrinkles the reader handles:

- **Fragmented tool calls:** `delta.ToolCalls[i]` arrive in pieces. The reader
  mirrors `internal/llm/anthropic/stream.go`: emits `EventToolUseStart` when a
  tool index first appears, `EventToolUseInputDelta` events for each argument
  fragment (carried in the event’s TextDelta field) as they arrive, and
  `EventToolUseStop` when that index ends (new index or stream end). The tool-loop
  reassembles the complete arguments from the fragments.
- **Usage:** OpenAI omits `usage` from streams unless asked. The request sets
  `StreamOptions{IncludeUsage: true}`; the terminal `EventMessageStop` carries
  both `InputTokens` and `OutputTokens` (OpenAI reports usage only on a final
  `include_usage` chunk).

Emit `llm.StreamEvent`s matching the existing `StreamEventType` vocabulary the
anthropic reader produces (message start/stop, text delta, tool-use), so the
tool-loop consumes both providers identically.

## 4. Factory + capabilities

In `cloudfactory.BuildCloudProvider`, add:

```go
case FlavorChatCompletions:
    return openai.NewClient(openai.Config{BaseURL: p.BaseURL, APIKey: apiKey, Model: p.Model}), nil
```

`Capabilities{SupportsTools: true, SupportsParallelTools: true, SupportsCaching: false, SupportsVision: true}`
(`SupportsCaching` off — compat endpoints vary). The adapter includes **image
translation** per [vision-input.md](./vision-input.md): a `BlockImage` becomes a
multi-part `image_url` content part — `ImageURL` passed through directly, or
`ImageData` as a `data:<MediaType>;base64,<ImageData>` URI. Vision plumbing is
built together with this provider (foundation first).

## 5. OpenAI-compatible endpoints

No auto-migration (decided). A user reaches Gemini, Groq, etc. by creating a
`chat_completions` profile with the right `base_url` + key + model.

**Common endpoints:**

| Provider | base_url | Notes |
|---|---|---|
| OpenAI | `(empty)` | Defaults to `api.openai.com` |
| Gemini | `https://generativelanguage.googleapis.com/v1beta/openai` | Requires Gemini API key |
| Groq | `https://api.groq.com/openai/v1` | Requires Groq API key |

**Setup flow** — store the key, then add a profile and activate it:

```bash
/cloud key <name> <API_KEY>
```

```yaml
# ~/.config/cercano/config.yaml
cloud_profiles:
  - name: gemini
    flavor: chat_completions
    base_url: https://generativelanguage.googleapis.com/v1beta/openai
    model: gemini-2.5-flash
```

Activate with `/cloud use <name>`.

SP2 ships no provider-specific base_url logic — it's all
`base_url + key + model`.

## 6. Testing

- **adapter** round-trips: text; single tool_use; tool_result; multiple/parallel
  tool calls; system prompt placement.
- **stream** delta-accumulation: fragmented `tool_calls` arguments across chunks →
  one complete `ToolInputRaw`; interleaved text deltas; usage from the final chunk.
- **client** against an `httptest` server (or `go-openai`'s `BaseURL` pointed at
  one) — no live network. Assert a tool-calling round-trip and a streamed one.
- **factory**: `chat_completions` → an `*openai.Client`; capabilities present.

## Out of scope

- OpenAI **Responses API** (sub-project 3) and **Bedrock** (sub-project 4).
- Explicit prompt-caching controls.
- The image **inbound path** (CLI attach + proto field) — see vision-input.md;
  this sub-project plumbs image *translation* in the adapter, not image *capture*.
- Auto-migrating legacy `google` configs (manual profile by decision).
- Per-endpoint tool-calling fidelity: some compat backends support tools poorly or
  not at all — that surfaces as a runtime error from the backend, not something
  this provider papers over.
