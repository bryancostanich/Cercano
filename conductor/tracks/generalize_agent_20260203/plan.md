# Track Plan: Generalize Agent Capabilities

This plan outlines the refactor to move from a task-specific "Unit Test" handler to a generic, intent-aware agentic architecture.

## Phase 1: Tool & Interface Generalization [checkpoint: bc1f444]

### Objective
Remove hardcoded task logic and rename components to be domain-agnostic.

### Tasks
- [x] Task: Generalize `CodeGenerator` interface. [6ca4f88]
    - [x] Update interface to accept `instruction string` and `code string`.
- [x] Task: Implement `GenericCodeGenerator`. [ae2ba49]
    - [x] Rename `internal/tools/unittest_handler.go` to `internal/tools/generic_generator.go`.
    - [x] Update `Generate` method to use the passed instruction.
    - [x] Implement a system prompt wrapper.
- [x] Task: Enhance `GoValidator`. [22cf287]
    - [x] Rename `internal/tools/validator.go` to `internal/tools/go_validator.go`.
    - [x] (Optional) Logic to choose `go test` vs `go build` based on input context.
- [x] Task: Verify with unit tests. [761afbe]
- [x] Task: Conductor - User Manual Verification 'Tool Generalization' (Protocol in workflow.md) [4f0a65c]

## Phase 2: Intent Classification [checkpoint: 2e62b1f]

### Objective
Enable the system to distinguish between "requests that need a loop" and "simple chat".

### Tasks
- [x] Task: Define Intent Constants. [a126892]
    - [x] Add `IntentCoding` and `IntentChat` to the `agent` package.
- [x] Task: Expand `prototypes.yaml`. [5fb9b8c]
    - [ ] Add examples for "Coding" (generate, fix, refactor) and "Chat" (explain, summarize, what is).
- [x] Task: Implement Intent Detection. [d17a814]
    - [x] Add `ClassifyIntent(request)` to the `SmartRouter` or `Agent`.
- [x] Task: Verify classification with tests. [4f8f485]
- [~] Task: Conductor - User Manual Verification 'Intent Classification' (Protocol in workflow.md)

## Phase 3: The `Agent` Orchestrator

### Objective
Implement the new top-level orchestrator and update the gRPC server.

### Tasks
- [x] Task: Create `internal/agent/agent.go`. [3986143]
    - [x] Implement `Agent.ProcessRequest(input, code)`.
    - [x] Logic: Route -> Classify Intent -> Choose Strategy (Loop vs Direct).
- [x] Task: Update gRPC Server. [45167cd]
    - [x] Update `internal/server/server.go` to call the new `Agent` instead of the `Router` directly.
- [~] Task: Verify End-to-End.
    - [ ] Run sandbox tests with generic instructions.
- [ ] Task: Conductor - User Manual Verification 'Agent Orchestrator' (Protocol in workflow.md)

## Phase 4: Regression & Cleanup

### Objective
Ensure existing functionality is preserved and old code is removed.

### Tasks
- [ ] Task: Remove old `UnitTestHandler` references.
- [ ] Task: Update README and documentation.
- [ ] Task: Final System Verification.
- [ ] Task: Conductor - User Manual Verification 'Final Verification' (Protocol in workflow.md)
