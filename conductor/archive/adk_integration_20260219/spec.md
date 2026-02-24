# Track Specification: ADK Agent Loop Integration

## 1. Job Title
Replace the hand-rolled GenerationCoordinator with Google ADK's LoopAgent primitive.

## 2. Overview
This track integrates selected components from `google.golang.org/adk` into the Cercano Go backend — specifically the `LoopAgent` for orchestration and `SessionService` for state management. The goal is to reduce maintenance burden on the coordinator loop while preserving Cercano's core differentiators: the embedding-based SmartRouter and the existing ModelProvider interface (Ollama + cloud).

**What ADK replaces:** `loop/coordinator.go` and the `ProgressFunc` callback threading pattern.

**What ADK does NOT touch:** `SmartRouter`, `ModelProvider` interface, `OllamaProvider`, `CloudModelProvider`, `langchaingo` wrappers, or the gRPC server surface.

## 3. Architecture Decision

ADK's `model.LLM` interface is NOT used. The Go SDK is Gemini-only and has no Ollama or Anthropic support. Instead, existing `ModelProvider` implementations are wrapped in thin `agent.Agent` adapter functions. ADK owns orchestration; Cercano owns model routing and provider implementations.

```
SmartRouter (unchanged)
    └── selects ModelProvider (unchanged: Ollama / CloudModelProvider)
            └── wrapped as agent.Agent adapter
                    └── LoopAgent orchestrates [GeneratorAgent → ValidatorAgent]
                            └── ValidatorAgent sets Escalate=true on go build success
```

## 4. Requirements

### 4.1 Dependency Integration
- Add `google.golang.org/adk` to `go.mod`.
- No other new top-level dependencies beyond what adk-go brings transitively.

### 4.2 Agent Adapter Layer
- Implement `NewGeneratorAgent(provider agent.ModelProvider) agent.Agent` — wraps a `ModelProvider.Process()` call as an ADK custom agent.
- Implement `NewValidatorAgent(validator tools.Validator, workDir string) agent.Agent` — runs `go build`, sets `event.Actions.Escalate = true` on success, yields error content on failure.
- Escalation logic (local → cloud after N failures) must be preserved. Implement via session state: the ValidatorAgent reads a failure counter from `session.State` and signals escalation; a parent selector swaps the generator provider.

### 4.3 LoopAgent Coordinator
- Implement `NewADKCoordinator` in `internal/loop/` that satisfies the existing `Coordinator` interface.
- Internally uses `loopagent.New(loopagent.Config{MaxIterations: 3, ...})`.
- Must preserve the existing backup/restore file behaviour during the loop.
- Must preserve the filename inference step (ask local model which file to target).

### 4.4 Streaming / Progress Reporting
- Replace `ProgressFunc` threading with ADK's `iter.Seq2[*session.Event, error]` event iteration.
- The gRPC `StreamProcessRequest` handler maps ADK events to `StreamProcessResponse` progress messages.
- The `ProgressFunc` type may be retained as an adapter shim at the gRPC boundary if simpler.

### 4.5 SessionService
- Introduce `session.InMemoryService()` as the initial backend.
- Wire session creation/retrieval into the gRPC server so that conversation history is maintained within a gRPC stream call.
- Persistent backend (GORM/SQLite) is **out of scope** for this track — deferred to a future session persistence track.

## 5. Acceptance Criteria
- [ ] `go test ./...` passes with no regressions.
- [ ] The full agentic loop (generate → validate → fix → escalate) works end-to-end via gRPC.
- [ ] Progress events are streamed correctly to the VS Code extension during the loop.
- [ ] The SmartRouter, ModelProvider, OllamaProvider, and CloudModelProvider are unchanged.
- [ ] `go build` compilation is still used as the validation signal (not an LLM critic).

## 6. Out of Scope
- ADK `model.LLM` / Gemini model backend.
- Persistent session storage (GORM/SQLite backend).
- ADK MemoryService / long-term memory.
- ADK A2A or MCP tooling.
- Any changes to the VS Code extension or gRPC proto.
