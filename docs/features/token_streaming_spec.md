# Token-Level LLM Streaming

## Overview
LLM output previously arrived all at once in the VS Code chat panel because the Ollama provider used `stream: false` — the entire response was buffered server-side before being sent to the client, creating a noticeable delay where the user saw nothing and then suddenly the full response. This feature delivers output incrementally as the LLM generates it, providing immediate feedback. It targets the chat path (where the delay is most noticeable) with the local Ollama provider.

## Design / Architecture
A new `StreamingModelProvider` interface extends `ModelProvider` with a `ProcessStream()` method, following the same pattern as `StreamableCoordinator` extending `Coordinator`. The agent uses a runtime type assertion (`if sp, ok := provider.(StreamingModelProvider)`) to stream when supported and fall back to blocking `Process()` otherwise. The existing `ModelProvider.Process()` is unchanged.

A new `TokenDelta` proto message (with a `content` field) carries tokens, kept distinct from `ProgressUpdate` for clean semantic separation: progress goes to the status bar, tokens go to markdown rendering. It was added as `token_delta = 3` to the `StreamProcessResponse` oneof. A callback pattern — `TokenFunc func(token string)`, parallel to the existing `ProgressFunc` — propagates tokens, avoiding channel lifecycle complexity.

Data flow:
```
Ollama (stream:true, newline-delimited JSON chunks)
 → OllamaProvider.ProcessStream() calls onToken per chunk
   → Agent.ProcessRequestStream() pipes TokenFunc callback
     → Server.StreamProcessRequest() sends TokenDelta proto messages
       → gRPC stream → VS Code client.ts dispatches onToken
         → extension.ts calls response.markdown(token) incrementally
```

The `final_response` is still sent at stream end with full output + metadata (used by the client for routing metadata, file changes, and validation errors); if tokens were already streamed, the client skips re-rendering the output text.

## Key behaviors / capabilities
- **Ollama `ProcessStream()`** — sends `stream: true`, reads newline-delimited JSON via `json.NewDecoder`, calls `onToken` per chunk, and accumulates/returns the complete response. A `Done bool` field was added to the `generateResponse` struct. Handles a nil `onToken` callback gracefully.
- **Agent chat-path streaming** — `ProcessRequestStream` gained a `tokenProgress TokenFunc` parameter; in the non-coding (chat) intent it streams tokens when the provider supports it, falling back to blocking `Process()` when `tokenProgress` is nil or the provider isn't streaming-capable.
- **gRPC piping** — `StreamProcessRequest` passes a `TokenFunc` that emits `TokenDelta` messages over the gRPC stream.
- **VS Code rendering** — `client.ts` added an `onToken` callback and handles `hasTokenDelta()` in its data handler; `extension.ts` tracks a `tokensReceived` flag and calls `response.markdown(token)` incrementally, skipping the final re-render if tokens were already streamed.

## Notable decisions / constraints
- **Chat path only** — the coding path uses the ADK LoopAgent which internally manages its own LLM calls; streaming tokens from within that loop is a more complex, separate concern (future enhancement).
- **Ollama (local) only** — the system is local-first; cloud-provider streaming via langchaingo can be added later using the same `StreamingModelProvider` interface.
- Unchanged: `adk_coordinator.go`, `conversation.go`, `router.go`, and cloud providers.
- Touched both proto stubs (Go via protoc, TypeScript via `npm run gen:proto`), the agent/llm/server Go layers, and the VS Code client/extension.
