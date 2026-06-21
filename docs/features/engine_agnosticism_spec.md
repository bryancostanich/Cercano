# AI Engine Agnosticism

## Overview

Cercano's local inference layer is decoupled from Ollama. Inference backends are
pluggable behind two interfaces — `InferenceEngine` and `EmbeddingService` — so
the agent, router, and coordinator depend on abstractions rather than on Ollama's
HTTP API. Adding a new backend (ONNX Runtime, Enso, or any future engine) requires
only implementing these interfaces; no changes to agent, router, or coordinator
code are needed.

Before this work, Ollama's HTTP API was called directly in three places:
`OllamaProvider` (generation), `SmartRouter` (embeddings), and `main.go` (wiring).
All Ollama-specific HTTP/JSON logic is now isolated in `internal/engine/ollama/`.

Shipped via PR #1 (branch `agnostic-agent`), merged 2026-03-25.

## Design / Architecture

The pre-existing `ModelProvider` interface (`Process` + `Name`) already abstracted
"something that handles a request." The gap was one level below: there was no
abstraction for the inference *runtime* a provider calls. This feature adds that
layer.

### Interfaces

Defined in `internal/engine/engine.go`:

`InferenceEngine`
- `Complete(ctx, model, prompt, systemPrompt) → string` — single-shot generation.
- `CompleteStream(ctx, model, prompt, systemPrompt, onToken) → string` — streaming
  generation; invokes `onToken` per token and returns the final accumulated text.
- `ListModels(ctx) → []ModelInfo` — available models on the engine, for validation
  and UI.
- `Name() → string`.

`EmbeddingService`
- `Embed(ctx, model, text) → []float64`.
- `Name() → string`.

`ModelInfo` is the shared model-descriptor type returned by `ListModels`.

### EngineRegistry

`internal/engine/registry.go` holds engines and embedders by name:

```go
type EngineRegistry struct {
    engines   map[string]InferenceEngine
    embedders map[string]EmbeddingService
}
```

It supports registering, retrieving, and listing engines, enabling runtime engine
discovery and future runtime engine switching. `main.go` uses it to wire engines
at startup.

### Ollama engine extraction

`internal/engine/ollama/ollama.go` implements both `InferenceEngine` and
`EmbeddingService`. It owns all Ollama HTTP client logic that previously lived
across the codebase:
- Generation logic moved from `internal/llm/ollama.go` into `Complete()`.
- Streaming logic moved from `internal/llm/ollama.go` into `CompleteStream()`.
- Embedding logic moved from `SmartRouter.GetEmbedding()` into `Embed()`.
- `ListModels()` queries Ollama's `/api/tags` endpoint.

### Provider and router refactor

Providers are now thin adapters over the engine layer:

```
SmartRouter (unchanged classification logic)
    └── uses EmbeddingService (injected; was hardcoded Ollama HTTP)
Agent (unchanged)
    └── selects ModelProvider (unchanged interface)
            ├── LocalModelProvider (refactored)
            │       └── delegates to InferenceEngine
            └── CloudModelProvider (unchanged, abstracted via langchaingo)
```

- `internal/llm/ollama.go` became `internal/llm/local_provider.go`. `OllamaProvider`
  was renamed `LocalModelProvider` and now delegates `Process`/`ProcessStream` to an
  injected `InferenceEngine` instead of holding HTTP client logic. Constructor:
  `NewLocalModelProvider(engine InferenceEngine, modelName string)`.
- `SmartRouter` accepts an `EmbeddingService` in its constructor. The
  `ollamaEmbeddingAPIURL` constant and the direct HTTP embedding call were removed;
  `GetEmbedding()` delegates to `EmbeddingService.Embed()`.
- `cmd/agent/main.go` creates an `OllamaEngine`, registers it in the
  `EngineRegistry`, constructs the `LocalModelProvider` from the engine, and passes
  the engine to `SmartRouter` as its `EmbeddingService`, then wires the provider
  into the coordinator and server.

## Key Behaviors / Capabilities

- Local inference is backend-agnostic. New engines plug in by implementing
  `InferenceEngine` and/or `EmbeddingService`.
- Streaming generation works end-to-end (server → gRPC → VS Code extension) via
  `InferenceEngine.CompleteStream()`.
- Runtime model switching is preserved through `LocalModelProvider.SetModelName()`
  (reachable via `/config`).
- The `StreamingModelProvider` interface is preserved on `LocalModelProvider`.
- SmartRouter classification works unchanged, now driven by an injected embedding
  service.
- Model enumeration is available via `ListModels()` for validation and UI.

## Notable Decisions / Constraints

- The abstraction was deliberately placed below `ModelProvider`, not as a
  replacement for it. `ModelProvider` was already a clean boundary; the new
  interfaces fill the missing runtime-level abstraction.
- All Ollama-specific code is confined to `internal/engine/ollama/`. `internal/llm/`
  and `internal/agent/` no longer import or call Ollama APIs directly.
- `CloudModelProvider` was left untouched — it is already abstracted through
  langchaingo.
- Deliberately out of scope: ONNX Runtime and Enso engine implementations (future
  tracks), any change to the `ModelProvider` interface, the ADK coordinator/adapters,
  the gRPC proto, the VS Code extension, and engine-level configuration UI in VS
  Code. These remained unchanged; the design makes the engine implementations
  trivial to add later.
