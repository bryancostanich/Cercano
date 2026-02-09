# Track Plan: Build the MVP of the Local-First AI Assistant

This plan outlines the phases and tasks required to build the MVP of the local-first AI assistant, following the project's workflow guidelines.

## Phase 1: Setup and Core gRPC Service

### Objective
Establish the foundational Go project structure, define the gRPC service for inter-process communication, and implement a basic server.

### Tasks
- [x] Task: Initialize Go project structure.
    - [x] Subtask: Create `go.mod` and initial directory layout.
    - [x] Subtask: Define basic `main.go` entry point.
- [x] Task: Define gRPC service contract (protobuf).
    - [x] Subtask: Define service methods for AI requests (e.g., `ProcessRequest`).
    - [x] Subtask: Define request and response message structures.
- [x] Task: Generate gRPC server/client stubs.
    - [x] Subtask: Configure protobuf compiler for Go.
    - [x] Subtask: Generate `*.pb.go` files from `.proto`.
- [x] Task: Implement basic gRPC server.
    - [x] Subtask: Write tests for gRPC server instantiation and basic request handling.
    - [x] Subtask: Implement server with a placeholder `ProcessRequest` method.
- [x] Task: Conductor - User Manual Verification 'Setup and Core gRPC Service' (Protocol in workflow.md)

## Phase 2: Semantic Router Logic (Embeddings)

### Objective
Implement a robust routing logic using semantic similarity (embeddings) to direct requests to appropriate local or cloud models. This approach is preferred over prompt-based classification due to the challenges of controlling local models to reliably classify and provide formatted output without unintended generation or hallucination.

### Tasks
- [x] Task: Design router interface.
    - [x] Subtask: Define interfaces for router and model providers (local/cloud).
- [x] Task: Select a local embedding model.
    - [x] Subtask: Research and choose a small, fast local embedding model (e.g., `nomic-embed-text`) available via Ollama.
- [x] Task: Create routing prototypes file.
    - [x] Subtask: Define a format for categorized example phrases (YAML).
    - [x] Subtask: Populate the file with representative phrases for `LocalModel` and `CloudModel`.
- [x] Task: Implement semantic router logic.
    - [x] Subtask: Implement `GetEmbedding` to call Ollama's embeddings API.
    - [x] Subtask: Implement Cosine Similarity math helper.
    - [x] Subtask: Implement `SelectProvider` to compare the request embedding against the prototypes.
- [x] Task: Create and run an integration test for semantic routing.
    - [x] Subtask: Validate that diverse prompts are routed correctly based on mathematical similarity.
- [x] Task: Update README with embedding model requirements.
- [x] Task: Conductor - User Manual Verification 'Semantic Router Logic' (Protocol in workflow.md)

### Phase 2 Refinement: Robustness & Fallback
- [x] Task: Expand routing prototypes.
    - [x] Subtask: Use local LLM to generate diverse phrasing variations for Local/Cloud categories.
    - [x] Subtask: Update `prototypes.yaml` with 50+ examples.
- [x] Task: Implement Router Fallback Logic.
    - [x] Subtask: Define a similarity threshold (e.g., 0.35).
    - [x] Subtask: Update `SelectProvider` to default to `CloudModel` if confidence is low.
- [x] Task: Refactor and harden tests.
    - [x] Subtask: Ensure integration tests do NOT use exact string matches from prototypes.
    - [x] Subtask: Add test cases for ambiguous queries to verify fallback behavior.

## Phase 3: Basic Local Model Integration (Unit Test Generation)

### Objective
Integrate the local AI model (Qwen3-coder) and establish the basic pipeline for generating unit tests.

### Tasks
- [x] Task: Select and integrate a local model for code analysis/generation. [d94a2bd]
    - [x] Subtask: Research suitable open-source or local-first models for test generation.
    - [x] Subtask: Write integration tests for the chosen local model.
    - [x] Subtask: Integrate the model into the Go application.
- [x] Task: Verify Smart Router Classification.
    - [x] Subtask: Validated that "Generate unit tests..." prompts are correctly routed to the Local Model.
- [x] Task: Create Sandbox Test Environment.
    - [x] Subtask: Created `test/sandbox` with a simple Calculator app.
    - [x] Subtask: Established a "Live Fire" test harness (`sandbox_test_generation_test.go`) to run generation against the sandbox.
- [x] Task: Initial End-to-End Verification.
    - [x] Subtask: Verified the model generates Go code.
    - [x] Subtask: Identified that one-shot generation is insufficient (issues with unused imports, markdown formatting).

## Phase 3.5: Agentic Self-Correction Loop [checkpoint: 8e76f6d]

### Objective
Implement an iterative "Generate -> Test -> Fix" loop using a Coordinator pattern. This decouples the "thinking" (Handler) from the "doing" (File I/O, Test Execution) and ensures generated code compiles and passes.

### Tasks
- [x] Task: Define Component Interfaces. [925b38b]
    - [x] Subtask: Define `Validator` interface for running tests/builds.
    - [x] Subtask: Define `CodeGenerator` interface (satisfied by `UnitTestHandler`).
- [x] Task: Implement Validator. [51feac1]
    - [x] Subtask: Create `internal/agent/validator.go` to execute `go test -c` and capture stderr.
- [x] Task: Enhance `UnitTestHandler` with Fix Capability. [d8b7012]
    - [x] Subtask: Add `Fix(ctx, code, errorMsg)` method to `UnitTestHandler` that prompts the model to correct specific errors.
- [x] Task: Implement `GenerationCoordinator`. [bd10c17]
    - [x] Subtask: Create `internal/agent/coordinator.go` to orchestrate the Retry Loop (Generate -> Write -> Validate -> Fix).
- [x] Task: Verify Self-Correction with Sandbox. [ac527a9]
    - [x] Subtask: Run `TestSandbox_GenerateAndRunTests` using the new Coordinator and verify it auto-corrects errors (e.g., unused imports).
- [x] Task: Conductor - User Manual Verification 'Self-Correction Loop' (Protocol in workflow.md)

## Phase 4: IDE Abstraction Layer (VS Code/Antigravity Compatibility)

### Objective
Develop a decoupled, VS Code-compatible abstraction layer that communicates with the core Go application via gRPC to enable integration with Antigravity.

### Tasks
- [ ] Task: Set up VS Code extension development environment.
    - [ ] Subtask: Initialize a new VS Code extension project.
    - [ ] Subtask: Configure gRPC client for communication with Go backend.
- [ ] Task: Implement basic gRPC client in the IDE abstraction layer.
    - [ ] Subtask: Write tests for gRPC client connection and basic request/response.
    - [ ] Subtask: Implement client to call the Go backend's `ProcessRequest` method.
- [ ] Task: Implement IDE command for "Generate Unit Tests".
    - [ ] Subtask: Write tests for the IDE command, ensuring it captures context and sends to backend.
    - [ ] Subtask: Implement command that captures selected code/file context and sends to the Go backend via gRPC.
    - [ ] Subtask: Display the generated unit tests in the IDE.
- [ ] Task: Conductor - User Manual Verification 'IDE Abstraction Layer (VS Code/Antigravity Compatibility)' (Protocol in workflow.md)

## vNext

### Tasks
- [ ] Task: Embed `llama.cpp` for a self-contained application.
    - [ ] Subtask: Research and select a Go binding for `llama.cpp`.
    - [ ] Subtask: Integrate the `llama.cpp` library into the Go project using Cgo.
    - [ ] Subtask: Implement a mechanism to package and distribute the Phi-2 model file with the application.
    - [ ] Subtask: Update the `SmartRouter` to use the embedded `llama.cpp` runtime instead of the Ollama API.
- [ ] Task: Add contextual information to the router prompt.
    - [ ] Subtask: Design a mechanism to gather context from the IDE.
    - [ ] Subtask: Update the `SmartRouter` to include this context in the prompt.
