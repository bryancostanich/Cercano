# Track Specification: Project Architecture Refactor and Cleanup

## 1. Job Title
Refactor and clean up existing architecture/code structure to improve clarity and organization.

## 2. Overview
This track focuses on reorganizing the codebase to resolve current structural confusion. The primary goals are to establish a clear "Server/Client" directory hierarchy, unify all code under the `source/` directory, and redefine component boundaries (Router vs. Coordinator) to accurately reflect their roles in the agentic system.

## 3. Structural Requirements

### 3.1 Unification under `source/`
*   Move the core Go application into `source/server/`.
*   Move the VS Code and Zed extensions into `source/clients/`.
*   Establish a consistent project root layout.

### 3.2 Component Clarification (Server)
*   **Agent Logic (formerly Router):** Formalize the "Agent" as the primary logic brain responsible for intent classification and decision-making. 
*   **Workflow Execution (formerly Coordinator):** Redefine the "Coordinator" as a specific workflow executor (e.g., Test Generation Loop) that is invoked by the Agent.
*   **Internal Package Reorganization:** Break down the current `internal/agent` and `internal/router` folders into logical domains that follow this new separation of concerns.

### 3.3 Interface Consolidation
*   Ensure gRPC contracts (`proto/`) are centrally located and clearly shared between `server/` and `clients/`.

## 4. Requirements

### 4.1 Functional Requirements
*   **FR1:** All functional features (unit test generation, self-correction loop) MUST remain fully operational after the refactor.
*   **FR2:** The VS Code extension MUST be updated to point to any changed paths or gRPC connection settings.
*   **FR3:** The build and test commands MUST be updated to reflect the new directory structure.

### 4.2 Non-Functional Requirements
*   **NFR1 (Maintainability):** The new structure MUST eliminate current ambiguity between the Router, Agent, and Coordinator roles.
*   **NFR2 (Scalability):** The structure MUST easily accommodate new clients (e.g., a CLI or Web client) and new server-side capabilities.

## 5. Acceptance Criteria
*   [ ] All unit and integration tests pass in the new structure.
*   [ ] **Manual Verification:** The VS Code extension can successfully connect to the backend and generate tests end-to-end in the Extension Development Host.
*   [ ] The `source/` directory contains `server/`, `clients/`, and `proto/` subdirectories.
*   [ ] Documentation (READMEs, conductor plans) is updated to match the new structure.

## 6. Out of Scope
*   Adding new functional features to the agent or router.
*   Detailed implementation of the Zed extension (beyond moving existing scaffold).
