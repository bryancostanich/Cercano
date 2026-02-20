# Track Plan: ADK Agent Loop Integration

## Phase 1: Dependency & Package Setup

### Objective
Add adk-go to the project and establish the new package structure without breaking anything.

### Tasks
- [x] Task: Add `google.golang.org/adk` to `go.mod` and run `go mod tidy`. [72705c7]
- [x] Task: Create `internal/loop/adapters/` package skeleton for agent adapter code. [72705c7]
- [x] Task: Verify existing tests still pass after dependency addition. [72705c7]
- [x] Task: Conductor - User Manual Verification 'Dependency & Package Setup' (Protocol in workflow.md)

## Phase 2: Agent Adapter Layer

### Objective
Wrap existing ModelProvider and Validator implementations as ADK agent.Agent types.

### Tasks
- [x] Task: Implement `NewGeneratorAgent(local, cloud agent.ModelProvider) (agent.Agent, error)`.
    - [x] Red phase: Write unit tests asserting correct event emission for success and error cases.
    - [x] Green phase: Implement the adapter wrapping `provider.Process()`.
- [x] Task: Implement `NewValidatorAgent(validator tools.Validator, workDir string, threshold int) (agent.Agent, error)`.
    - [x] Red phase: Write unit tests asserting `Escalate=true` on build success, error content on failure.
    - [x] Green phase: Implement the adapter wrapping `validator.Validate()`.
- [x] Task: Implement escalation state logic via `session.State` failure counter.
    - [x] Red phase: Write tests asserting provider switch after threshold failures.
    - [x] Green phase: Implement counter read/write in ValidatorAgent; wire provider selection.
- [x] Task: Conductor - User Manual Verification 'Agent Adapter Layer' (Protocol in workflow.md)

## Phase 3: LoopAgent Coordinator

### Objective
Replace GenerationCoordinator with an ADK-backed implementation satisfying the existing Coordinator interface.

### Tasks
- [ ] Task: Implement `NewADKCoordinator` in `internal/loop/`.
    - [ ] Red phase: Port existing coordinator tests to the new implementation.
    - [ ] Green phase: Implement using `loopagent.New(...)` with the adapters from Phase 2.
    - [ ] Preserve backup/restore file behaviour and filename inference step.
- [ ] Task: Wire `NewADKCoordinator` into `cmd/agent/main.go` replacing `NewGenerationCoordinator`.
- [ ] Task: Run full test suite; fix any regressions.
- [ ] Task: Conductor - User Manual Verification 'LoopAgent Coordinator' (Protocol in workflow.md)

## Phase 4: Streaming & SessionService

### Objective
Replace ProgressFunc threading with ADK event iteration and introduce in-memory SessionService.

### Tasks
- [ ] Task: Refactor progress reporting to consume `iter.Seq2[*session.Event, error]`.
    - [ ] Map ADK events to existing `StreamProcessResponse` progress messages at gRPC boundary.
- [ ] Task: Introduce `session.InMemoryService()` in `cmd/agent/main.go`.
    - [ ] Wire session create/retrieve into gRPC server per-stream-call.
- [ ] Task: Red/Green tests for streaming event delivery.
- [ ] Task: Conductor - User Manual Verification 'Streaming & SessionService' (Protocol in workflow.md)
