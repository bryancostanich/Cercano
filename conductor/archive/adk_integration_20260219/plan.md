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

## Phase 3: LoopAgent Coordinator [checkpoint: 50b73cf]

### Objective
Replace GenerationCoordinator with an ADK-backed implementation satisfying the existing Coordinator interface.

### Tasks
- [x] Task: Implement `NewADKCoordinator` in `internal/loop/`.
    - [x] Red phase: Port existing coordinator tests to the new implementation.
    - [x] Green phase: Implement using `loopagent.New(...)` with the adapters from Phase 2.
    - [x] Preserve backup/restore file behaviour and filename inference step.
- [x] Task: Wire `NewADKCoordinator` into `cmd/agent/main.go` replacing `NewGenerationCoordinator`.
- [x] Task: Run full test suite; fix any regressions.
- [x] Task: Conductor - User Manual Verification 'LoopAgent Coordinator' (Protocol in workflow.md)

## Phase 4: Streaming & SessionService [checkpoint: 58969fc]

### Objective
Replace ProgressFunc threading with ADK event iteration and introduce in-memory SessionService.

### Tasks
- [x] Task: Refactor progress reporting to consume `iter.Seq2[*session.Event, error]`.
    - [x] Map ADK events to existing `StreamProcessResponse` progress messages at gRPC boundary.
- [x] Task: Introduce `session.InMemoryService()` in `cmd/agent/main.go`.
    - [x] Wire shared session service into ADKCoordinator and ConversationStore.
- [x] Task: Add `StreamableCoordinator` interface and `MapEventToProgress` helper.
- [x] Task: Red/Green tests for streaming event delivery.
- [x] Task: Conductor - User Manual Verification 'Streaming & SessionService' (Protocol in workflow.md)

## Phase 5: Conversation History [checkpoint: 821074f]

### Objective
Add server-side conversation tracking so multi-turn requests can resolve references from prior turns.

### Tasks
- [x] Task: Add `conversation_id` field to proto; regenerate Go and JS/TS stubs.
- [x] Task: Implement `ConversationStore` with `AppendTurn`, `LoadHistory`, and `CompactResponse`.
    - [x] Red/Green TDD: 7 tests covering round-trip, depth limit, empty ID, multi-conversation isolation.
- [x] Task: Integrate into Agent via functional options (`WithConversationStore`).
    - [x] Classification uses original input (no history pollution).
    - [x] Execution uses augmented input (LLM resolves references).
    - [x] Storage uses original input (prevents recursive accumulation).
    - [x] Red/Green TDD: 4 tests covering injection, storage, no-ID, nil-store.
- [x] Task: Map `ConversationID` in server.go `mapRequest`.
- [x] Task: Wire `ConversationStore` in `main.go`.
- [x] Task: Extension generates UUID `conversationId` and passes with each request.
- [x] Task: Add referential coding prototypes and suggestion-seeking chat prototypes.
- [x] Task: Fix provider routing fallback to default to local.
- [x] Task: Conductor - User Manual Verification 'Conversation History' (Protocol in workflow.md)
