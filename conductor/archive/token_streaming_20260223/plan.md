# Track Plan: Token-Level LLM Streaming

LLM output arrives all at once in the VS Code chat panel because the Ollama provider uses `stream: false`. The entire response is buffered server-side before being sent to the client. For chat interactions this creates a noticeable delay where the user sees nothing, then suddenly the full response appears. Token-level streaming delivers output incrementally as the LLM generates it, providing immediate feedback.

## [x] Phase 1: End-to-End Token Streaming (Chat Path)

### Scope
- **Chat path only** -- this is where users notice the delay most. The coding path uses the ADK LoopAgent which internally manages its own LLM calls; streaming tokens from within that loop is a separate, more complex concern (future enhancement).
- **Ollama (local) only** -- the system is local-first. Cloud provider streaming via langchaingo can be added later using the same interface.

### Key Design Decisions
- **New `StreamingModelProvider` interface** -- follows the exact pattern of `StreamableCoordinator` extending `Coordinator`. Runtime type assertion (`if sp, ok := provider.(StreamingModelProvider)`) in the agent. Existing `ModelProvider.Process()` unchanged.
- **New `TokenDelta` proto message** -- distinct from `ProgressUpdate` (which carries status messages). Clean semantic separation: progress goes to status bar, tokens go to markdown rendering.
- **Callback pattern** -- `TokenFunc func(token string)` parallel to existing `ProgressFunc func(message string)`. Consistent with established patterns, avoids channel lifecycle complexity.
- **Final response still sent** -- the `final_response` is still sent at stream end with full output + metadata. The client uses it for routing metadata, file changes, validation errors. If tokens were streamed, the client skips re-rendering the output text.

### Data Flow

```
Ollama (stream:true, newline-delimited JSON chunks)
  -> OllamaProvider.ProcessStream() calls onToken per chunk
    -> Agent.ProcessRequestStream() pipes TokenFunc callback
      -> Server.StreamProcessRequest() sends TokenDelta proto messages
        -> gRPC stream delivers to VS Code client
          -> client.ts dispatches onToken callback
            -> extension.ts calls response.markdown(token) incrementally
```

### Tasks

#### [x] Task 1: Proto -- Add `TokenDelta` message
- Added `TokenDelta` message with `content` field to `agent.proto`
- Added `token_delta = 3` to `StreamProcessResponse` oneof
- Regenerated Go stubs (`protoc`) and TypeScript stubs (`npm run gen:proto`)

#### [x] Task 2: Agent interfaces -- `StreamingModelProvider` and `TokenFunc`
- Added `TokenFunc func(token string)` callback type to `streaming.go`
- Added `StreamingModelProvider` interface extending `ModelProvider` with `ProcessStream()`
- Tests: interface satisfaction, token ordering with accumulated output

#### [x] Task 3: Ollama Provider -- Implement `ProcessStream()`
- Added `Done bool` field to `generateResponse` struct
- Implemented `ProcessStream()` on `OllamaProvider`: sends `stream: true`, reads newline-delimited JSON via `json.NewDecoder`, calls `onToken` per chunk, accumulates and returns complete response
- Handles nil `onToken` gracefully
- Tests: mock HTTP server returning chunked JSON, nil callback safety, interface satisfaction

#### [x] Task 4: Agent -- Wire streaming in chat path
- Extended `ProcessRequestStream` signature with `tokenProgress TokenFunc` parameter
- In the chat path (non-coding intent), runtime type assertion checks for `StreamingModelProvider` -- streams tokens when available, falls back to blocking `Process()` when tokenProgress is nil or provider doesn't support streaming
- Updated all callers (server.go, existing tests) to pass new parameter
- Tests: chat with token streaming, fallback to non-streaming, nil token callback

#### [x] Task 5: gRPC Server -- Pipe `TokenDelta` messages
- Updated `StreamProcessRequest` to pass a `TokenFunc` callback that sends `TokenDelta` proto messages over the gRPC stream

#### [x] Task 6: VS Code Client -- Handle `token_delta` events
- Added `onToken` callback parameter to `processStream()` in `client.ts`
- Handles `hasTokenDelta()` in the data handler, dispatches to `onToken`

#### [x] Task 7: VS Code Extension -- Incremental markdown rendering
- Tracks `tokensReceived` flag; calls `response.markdown(token)` incrementally per token
- Skips final output re-render if tokens were already streamed (fallback for non-streaming providers)

### Files Modified

| File | Change |
|------|--------|
| `source/proto/agent.proto` | Add `TokenDelta` message, add `token_delta = 3` to `StreamProcessResponse` oneof |
| `source/server/pkg/proto/*.go` | Regenerated via protoc |
| `source/clients/vscode/src/proto/*` | Regenerated via `npm run gen:proto` |
| `source/server/internal/agent/streaming.go` | Add `TokenFunc` type and `StreamingModelProvider` interface |
| `source/server/internal/agent/streaming_test.go` | Interface satisfaction + token ordering tests |
| `source/server/internal/llm/ollama.go` | Add `Done bool` to `generateResponse`, implement `ProcessStream()` |
| `source/server/internal/llm/ollama_test.go` | ProcessStream tests with mock HTTP server |
| `source/server/internal/agent/agent.go` | Add `tokenProgress TokenFunc` param, streaming dispatch in chat path |
| `source/server/internal/agent/agent_test.go` | Token streaming tests, update existing callers |
| `source/server/internal/server/server.go` | Pass `TokenFunc` callback sending `TokenDelta` messages |
| `source/clients/vscode/src/client.ts` | Add `onToken` callback, handle `hasTokenDelta()` |
| `source/clients/vscode/src/extension.ts` | Incremental `response.markdown(token)`, skip re-render if streamed |

**Unchanged:** `adk_coordinator.go`, `conversation.go`, `router.go`, cloud providers
