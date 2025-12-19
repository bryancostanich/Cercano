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

## Phase 2: Smart Router Logic

### Objective
Implement the intelligent routing logic to direct requests to appropriate local or cloud models based on predefined criteria.

### Tasks
- [x] Task: Design router interface.
    - [x] Subtask: Define interfaces for router and model providers (local/cloud).
- [x] Task: Select a local model for routing classification.
    - [x] Subtask: Research and choose a small, fast local model suitable for this classification task.
- [x] Task: Create routing guidelines file.
    - [x] Subtask: Define a format for the guidelines file (Markdown).
    - [x] Subtask: Populate the guidelines file with conceptual rules for `LocalModel` and `CloudModel` execution.
- [x] Task: Implement model-driven router with guidelines.
    - [x] Subtask: Write tests for the model-driven router's decision-making based on the guidelines.
    - [x] Subtask: Implement the `SmartRouter` to load the `router_guidelines.md` file.
    - [x] Subtask: Update the `SelectProvider` method to construct a prompt (using the guidelines) and query the local routing model.
    - [x] Subtask: Update logic to parse the model's response and select the correct `ModelProvider`.
- [x] Task: Create README for the Go application.
    - [x] Subtask: Add installation instructions for Ollama and Phi-2 to the README.
- [x] Task: Conductor - User Manual Verification 'Smart Router Logic' (Protocol in workflow.md)

## Phase 3: Local Model Integration (Unit Test Generation)

### Objective
Integrate a local AI model specifically for the task of generating unit tests.

### Tasks
- [ ] Task: Select and integrate a local model for code analysis/generation.
    - [ ] Subtask: Research suitable open-source or local-first models for test generation.
    - [ ] Subtask: Write integration tests for the chosen local model.
    - [ ] Subtask: Integrate the model into the Go application.
- [ ] Task: Implement unit test generation handler.
    - [ ] Subtask: Write tests for the unit test generation handler.
    - [ ] Subtask: Implement logic to process code, call local model, and format output.
- [ ] Task: Conductor - User Manual Verification 'Local Model Integration (Unit Test Generation)' (Protocol in workflow.md)

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
