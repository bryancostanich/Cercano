# Track Specification: AI Engine Agnosticism

## 1. Job Title
Abstract the local inference layer so Cercano is not coupled to Ollama and can support pluggable inference backends.

## 2. Overview
Cercano currently hardcodes Ollama as its local inference engine. The Ollama HTTP API is called directly in three places: OllamaProvider (generation), SmartRouter (embeddings), and main.go (wiring). This track introduces clean abstraction boundaries — `InferenceEngine` and `EmbeddingService` interfaces — so that Cercano can support multiple local inference backends (Ollama, ONNX Runtime, Enso, and future engines) without modifying the agent, router, or coordinator logic.

This track focuses on creating the abstraction layer and refactoring the existing Ollama integration to conform to it. Actual ONNX/Enso implementations are out of scope — they become trivial to add once the interfaces exist.

**What changes:** `internal/llm/` package structure, SmartRouter's embedding dependency, main.go wiring, and provider construction.

**What does NOT change:** The `ModelProvider` interface itself (it's already clean), the SmartRouter classification logic, the ADK coordinator/adapters, the gRPC server surface, or the VS Code extension.

## 3. Architecture Decision

The `ModelProvider` interface (`Process` + `Name`) is a good abstraction for "something that handles a request." The problem is one level below: there's no abstraction for the *inference runtime* that a provider calls. OllamaProvider talks directly to Ollama's HTTP API, and SmartRouter talks directly to Ollama's embedding API.

The solution introduces two new interfaces:

```
InferenceEngine (new)
├── Complete(ctx, model, prompt, systemPrompt) → string
├── CompleteStream(ctx, model, prompt, systemPrompt, onToken) → string
├── ListModels(ctx) → []ModelInfo
└── Name() → string

EmbeddingService (new)
├── Embed(ctx, model, text) → []float64
└── Name() → string
```

Providers become thin adapters that translate between `ModelProvider` requests and `InferenceEngine` calls:

```
SmartRouter (unchanged logic)
    └── uses EmbeddingService (injected, was hardcoded Ollama HTTP)
Agent (unchanged)
    └── selects ModelProvider (unchanged interface)
            └── LocalModelProvider (refactored)
                    └── delegates to InferenceEngine (new)
            └── CloudModelProvider (unchanged, already abstracted via langchaingo)
```

An `EngineRegistry` provides runtime engine discovery and construction:

```go
type EngineRegistry struct {
    engines map[string]InferenceEngine
    embedders map[string]EmbeddingService
}
```

## 4. Requirements

### 4.1 InferenceEngine Interface
- Define `InferenceEngine` interface in `internal/engine/engine.go`.
- Methods: `Complete`, `CompleteStream`, `ListModels`, `Name`.
- `Complete` takes context, model name, prompt, and system prompt; returns generated text.
- `CompleteStream` adds an `onToken` callback for streaming; returns final accumulated text.
- `ListModels` returns available models on the engine (for validation and UI).

### 4.2 EmbeddingService Interface
- Define `EmbeddingService` interface in `internal/engine/engine.go`.
- Methods: `Embed(ctx, model, text) ([]float64, error)` and `Name() string`.
- SmartRouter must accept an `EmbeddingService` instead of making direct Ollama HTTP calls.

### 4.3 Ollama Engine Implementation
- Implement `OllamaEngine` in `internal/engine/ollama/` satisfying both `InferenceEngine` and `EmbeddingService`.
- Move all Ollama HTTP client logic (generation, streaming, embeddings) from `internal/llm/ollama.go` and `internal/agent/router.go` into this implementation.
- The existing `OllamaProvider` in `internal/llm/` becomes a thin adapter: it holds an `InferenceEngine` reference and translates `ModelProvider.Process()` into `engine.Complete()`.

### 4.4 LocalModelProvider Refactor
- Refactor `OllamaProvider` into a generic `LocalModelProvider` that delegates to any `InferenceEngine`.
- Preserve `SetModelName()` for runtime model switching.
- Preserve `StreamingModelProvider` interface support via `InferenceEngine.CompleteStream()`.

### 4.5 SmartRouter Decoupling
- Remove the hardcoded `ollamaEmbeddingAPIURL` constant and direct HTTP call from `SmartRouter.GetEmbedding()`.
- SmartRouter constructor accepts an `EmbeddingService` parameter.
- `GetEmbedding()` delegates to the injected `EmbeddingService.Embed()`.

### 4.6 Engine Registry
- Implement `EngineRegistry` in `internal/engine/registry.go`.
- Supports registering and retrieving engines by name.
- Used in `main.go` to wire engines at startup.
- Enables future runtime engine switching.

### 4.7 Main.go Wiring Update
- Update `cmd/agent/main.go` to:
  1. Create an `OllamaEngine`.
  2. Register it in the `EngineRegistry`.
  3. Create `LocalModelProvider` with the engine.
  4. Pass the engine's `EmbeddingService` to `SmartRouter`.

## 5. Acceptance Criteria
- [ ] `go test ./...` passes with no regressions.
- [ ] SmartRouter no longer imports or calls Ollama APIs directly.
- [ ] OllamaProvider (now LocalModelProvider) no longer contains Ollama HTTP logic.
- [ ] All Ollama-specific code is isolated in `internal/engine/ollama/`.
- [ ] A new engine can be added by implementing `InferenceEngine` and/or `EmbeddingService` — no changes to agent, router, or coordinator code required.
- [ ] Streaming still works end-to-end through the VS Code extension.
- [ ] Runtime model switching (`SetModelName`) still works.
- [ ] The gRPC proto and VS Code extension are unchanged.

## 6. Out of Scope
- ONNX Runtime engine implementation (future track).
- Enso engine implementation (future track).
- Changes to the `ModelProvider` interface.
- Changes to the ADK coordinator/adapters.
- Changes to the CloudModelProvider or langchaingo integration.
- Changes to the gRPC proto or VS Code extension.
- Engine-level configuration UI in VS Code.
