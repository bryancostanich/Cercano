# Track Plan: AI Engine Agnosticism [ARCHIVED — shipped via PR #1]

All 5 phases implemented and merged in PR #1 (branch: agnostic-agent, merged 2026-03-25).
Commits: 0748435, f43c976. Track was not updated during PR work; archiving as complete.

## Phase 1: Interface Definitions & Package Setup [complete]

### Objective
Define the core abstractions and establish the new package structure without breaking existing functionality.

### Tasks
- [x] Task: Create `internal/engine/` package with `engine.go` defining `InferenceEngine`, `EmbeddingService`, and `ModelInfo` types.
- [x] Task: Create `internal/engine/registry.go` with `EngineRegistry` (register, get, list engines).
    - [ ] Red phase: Write unit tests for registry operations (register, get, get-missing, list).
    - [ ] Green phase: Implement the registry.
- [x] Task: Verify existing tests still pass — no functional changes yet, only new files.
- [x] Task: Conductor - User Manual Verification 'Interface Definitions & Package Setup' (Protocol in workflow.md)

## Phase 2: Ollama Engine Implementation [complete]

### Objective
Extract all Ollama-specific HTTP/JSON logic into a standalone engine implementation.

### Tasks
- [x] Task: Create `internal/engine/ollama/ollama.go` implementing `InferenceEngine` and `EmbeddingService`.
    - [ ] Move generation HTTP logic from `internal/llm/ollama.go` into `Complete()`.
    - [ ] Move streaming HTTP logic from `internal/llm/ollama.go` into `CompleteStream()`.
    - [ ] Move embedding HTTP logic from `internal/agent/router.go` (`GetEmbedding`) into `Embed()`.
    - [ ] Implement `ListModels()` using Ollama's `/api/tags` endpoint.
- [x] Task: Write unit tests for the Ollama engine.
    - [ ] Red phase: Tests for Complete, CompleteStream, Embed, ListModels (use httptest server).
    - [ ] Green phase: Ensure all pass.
- [x] Task: Conductor - User Manual Verification 'Ollama Engine Implementation' (Protocol in workflow.md)

## Phase 3: Refactor LocalModelProvider [complete]

### Objective
Replace OllamaProvider with a generic LocalModelProvider that delegates to any InferenceEngine.

### Tasks
- [x] Task: Refactor `internal/llm/ollama.go` → `internal/llm/local_provider.go`.
    - [ ] Rename `OllamaProvider` to `LocalModelProvider`.
    - [ ] Replace internal HTTP client logic with `InferenceEngine` delegation.
    - [ ] Preserve `SetModelName()` and `StreamingModelProvider` interface.
    - [ ] Constructor becomes `NewLocalModelProvider(engine InferenceEngine, modelName string)`.
- [x] Task: Update all references to `OllamaProvider` / `NewOllamaProvider` across the codebase.
    - [ ] `cmd/agent/main.go`
    - [ ] `internal/server/server.go` (if referenced)
    - [ ] Test files
- [x] Task: Write/update unit tests for LocalModelProvider.
    - [ ] Red phase: Tests asserting engine delegation for Process, ProcessStream, SetModelName.
    - [ ] Green phase: Implement and pass.
- [x] Task: Verify full test suite passes — `go test ./...`.
- [x] Task: Conductor - User Manual Verification 'Refactor LocalModelProvider' (Protocol in workflow.md)

## Phase 4: Decouple SmartRouter from Ollama [complete]

### Objective
Remove Ollama's hardcoded embedding API from SmartRouter and inject EmbeddingService instead.

### Tasks
- [x] Task: Modify `SmartRouter` to accept `EmbeddingService` in its constructor.
    - [ ] Remove the `ollamaEmbeddingAPIURL` constant.
    - [ ] Remove the direct HTTP embedding call in `GetEmbedding()`.
    - [ ] Delegate to injected `EmbeddingService.Embed()`.
- [x] Task: Update `NewSmartRouter()` signature and all call sites.
    - [ ] `cmd/agent/main.go` — pass `OllamaEngine` as `EmbeddingService`.
    - [ ] Test files — pass mock or test `EmbeddingService`.
- [x] Task: Write/update unit tests for SmartRouter embedding delegation.
    - [ ] Red phase: Test that SmartRouter calls EmbeddingService, not Ollama directly.
    - [ ] Green phase: Implement and pass.
- [x] Task: Verify full test suite passes — `go test ./...`.
- [x] Task: Conductor - User Manual Verification 'Decouple SmartRouter from Ollama' (Protocol in workflow.md)

## Phase 5: Wiring & Integration [complete]

### Objective
Update main.go to use the engine registry and verify the full system works end-to-end.

### Tasks
- [x] Task: Update `cmd/agent/main.go` to use the new architecture.
    - [ ] Create `OllamaEngine` with configured URL.
    - [ ] Register in `EngineRegistry`.
    - [ ] Create `LocalModelProvider` with the engine.
    - [ ] Pass engine as `EmbeddingService` to `SmartRouter`.
    - [ ] Pass `LocalModelProvider` to coordinator and server.
- [x] Task: Verify streaming works end-to-end (server → gRPC → VS Code).
- [x] Task: Verify runtime model switching works (`SetModelName` via `/config`).
- [x] Task: Verify SmartRouter classification works with injected embedding service.
- [x] Task: Clean up dead code — remove any orphaned Ollama-specific types/functions from `internal/llm/` and `internal/agent/`.
- [x] Task: Run full test suite — `go test ./...`.
- [x] Task: Conductor - User Manual Verification 'Wiring & Integration' (Protocol in workflow.md)
