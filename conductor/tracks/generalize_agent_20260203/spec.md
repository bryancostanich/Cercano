# Track Specification: Generalize Agent Capabilities

## 1. Job Title
Refactor the backend to support a generic, agentic workflow for any coding task, removing the hardcoded "Unit Test" specialization.

## 2. Overview
The current backend is overly specialized for generating unit tests. This track aims to introduce a true `Agent` orchestrator that can handle any coding request. It will generalize the `UnitTestHandler` into a `GenericCodeGenerator`, implement an intent classifier to distinguish between "Coding" and "Chat" tasks, and re-architect the call flow to make the `Coordinator` loop a dynamic strategy rather than a hardcoded path.

## 3. Requirements

### 3.1 The `Agent` Orchestrator
*   **New Component:** Create `internal/agent/agent.go`.
*   **Responsibility:** It serves as the primary entry point for the gRPC server.
*   **Orchestration:** It calls the `Router` to select a model (Local/Cloud) and determines the execution strategy (Loop vs. Direct).

### 3.2 Generalization of Tools
*   **Generic Handler:** Rename/Refactor `UnitTestHandler` to `GenericCodeGenerator`.
    *   Remove hardcoded "Write unit tests" prompts.
    *   Accept the user's prompt as the primary instruction.
    *   Wrap prompts with a system persona ("You are an expert Go developer").
*   **Generic Validator:** Rename `GoTestValidator` to `GoValidator`. It should support `go build` (for non-test code) and `go test` (for test code). *Constraint: For this track, defaulting to `go test` or `go build` based on filename is acceptable.*

### 3.3 Intent Classification
*   **Strategy Selection:** The system must decide whether to run the **Coordinator Loop** (for coding) or a **Direct Call** (for chat/questions).
*   **Implementation:** Expand `prototypes.yaml` and `SmartRouter` logic to classify intent (`IntentCoding` vs `IntentChat`) using embedding similarity.

### 3.4 Architecture Flow
*   **Current:** Server -> Router -> UnitTestHandler -> LLM.
*   **Target:** Server -> Agent -> (Router + Intent) -> [Coordinator -> GenericGen -> Validator] OR [GenericGen].

## 4. Architecture Impact
*   **Refactor:** Significant changes to `internal/agent` and `internal/tools`.
*   **Interface Update:** `CodeGenerator` interface might need to accept `systemPrompt` or `userInstruction`.

## 5. Acceptance Criteria
*   [ ] User can ask "Write a function to add two numbers" and the system generates it using the Loop (detects Coding intent).
*   [ ] User can ask "Explain this code" and the system answers directly (detects Chat intent).
*   [ ] User can ask "Write unit tests" and it still works (regression test).
*   [ ] `UnitTestHandler` is deleted/renamed.

## 6. Out of Scope
*   Advanced multi-file editing.
*   New cloud integrations (handled in separate track).
