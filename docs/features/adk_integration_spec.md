# ADK Agent Loop Integration

## Overview

Cercano's hand-rolled `GenerationCoordinator` was replaced with Google ADK's `LoopAgent` orchestration primitive plus its `SessionService` for state management. This reduced maintenance burden on the agentic generate/validate/fix loop while preserving Cercano's differentiators — the embedding-based `SmartRouter` and the `ModelProvider` interface (Ollama + cloud). ADK now owns orchestration; Cercano continues to own model routing and provider implementations.

## Design / Architecture

ADK's `model.LLM` interface is deliberately *not* used — the ADK Go SDK is Gemini-only with no Ollama or Anthropic support. Instead, existing `ModelProvider` implementations are wrapped in thin `agent.Agent` adapter functions, keeping model routing under Cercano's control.

```
SmartRouter (unchanged)
    └── selects ModelProvider (unchanged: Ollama / CloudModelProvider)
            └── wrapped as agent.Agent adapter
                    └── LoopAgent orchestrates [GeneratorAgent → ValidatorAgent]
                            └── ValidatorAgent sets Escalate=true on go build success
```

Key components:
- **Generator adapter** — `NewGeneratorAgent(local, cloud agent.ModelProvider)` wraps a `ModelProvider.Process()` call as an ADK custom agent.
- **Validator adapter** — `NewValidatorAgent(validator tools.Validator, workDir string, threshold int)` runs `go build`, sets `event.Actions.Escalate = true` on success, and yields error content on failure. `go build` compilation remains the validation signal (not an LLM critic).
- **ADK coordinator** — `NewADKCoordinator` in `internal/loop/` satisfies the existing `Coordinator` interface and internally uses `loopagent.New(loopagent.Config{MaxIterations: 3, ...})`. It preserves the prior backup/restore file behaviour and the filename-inference step (asking the local model which file to target).
- **Streaming** — `ProgressFunc` callback threading was replaced with ADK's `iter.Seq2[*session.Event, error]` event iteration. A `StreamableCoordinator` interface plus a `MapEventToProgress` helper map ADK events to `StreamProcessResponse` progress messages at the gRPC boundary.
- **Session** — `session.InMemoryService()` is the backend, wired into the gRPC server and shared with the coordinator and conversation store.

## Key Behaviors / Capabilities

- Full agentic loop (generate → validate → fix → escalate) runs end-to-end via gRPC.
- Local→cloud escalation after N failures is preserved via a failure counter stored in `session.State`; a parent selector swaps the generator provider once the threshold is hit.
- Progress events stream to the VS Code extension during the loop.
- Server-side conversation history: a `conversationId` (UUID generated per-request by the extension) keys a `ConversationStore` with `AppendTurn`, `LoadHistory`, and `CompactResponse`. Classification uses original input (no history pollution), execution uses augmented input (so the LLM can resolve references to prior turns), and storage uses original input (prevents recursive accumulation). Integrated via functional options (`WithConversationStore`).
- `SmartRouter`, `ModelProvider`, `OllamaProvider`, and `CloudModelProvider` are unchanged.

## Notable Decisions / Constraints

- ADK `model.LLM` / Gemini backend is intentionally avoided — model backends stay in Cercano's `ModelProvider` layer.
- Session storage is in-memory only; persistent backend (GORM/SQLite) was deferred to a future session-persistence track.
- ADK MemoryService / long-term memory, and ADK A2A / MCP tooling, are out of scope.
- The `ProgressFunc` type may be retained as an adapter shim at the gRPC boundary where simpler.
- A multi-conversation isolation guarantee and a configurable history depth limit are enforced in the store.
