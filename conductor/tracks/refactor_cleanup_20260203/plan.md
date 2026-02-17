# Track Plan: Project Architecture Refactor and Cleanup

This plan outlines the phases and tasks required to refactor the project structure and clarify component roles, following the project's workflow guidelines.

## Phase 1: Directory Restructuring (Root & source/)

### Objective
Unify all project code under the `source/` directory and establish the `server/` vs. `clients/` hierarchy.

### Tasks
- [x] Task: Create new directory structure. [baf1fd6]
    - [x] Create `source/server/`.
    - [x] Create `source/clients/`.
- [x] Task: Move IDE extensions to `source/clients/`. [c48cf73]
    - [x] Move `vscode-extension/` to `source/clients/vscode/`.
    - [x] Move `zed-extension/` to `source/clients/zed/`.
- [x] Task: Move core Go code to `source/server/`. [8a9f6b5]
    - [x] Move existing files from `source/` (except `proto/` and `clients/`) to `source/server/`.
- [x] Task: Update Go module and imports. [18091a0]
    - [ ] Update `go.mod` if necessary.
    - [ ] Run `go fmt ./...` and fix import paths across the backend.
- [x] Task: Update VS Code extension configuration.
    - [x] Subtask: Update `package.json` paths, `tsconfig.json`, and `launch.json` to reflect the new location.
    - [x] Subtask: Verify `npm run compile` still works.
- [ ] Task: Conductor - User Manual Verification 'Directory Restructuring' (Protocol in workflow.md)

## Phase 2: Component Role Clarification (Server Refactor)

### Objective
Rename and reorganize internal packages to accurately reflect the "Agent" (logic) and "Coordinator" (executor) roles.

### Tasks
- [ ] Task: Formalize 'Agent' domain.
    - [ ] Create `source/server/internal/agent/` (if not exists or needs move).
    - [ ] Move/Refactor Router logic into the Agent package as the "brain".
- [ ] Task: Define 'Workflows' domain (Coordinator).
    - [ ] Create `source/server/internal/workflows/` (or similar).
    - [ ] Move the `GenerationCoordinator` into the workflows package.
- [ ] Task: Update gRPC Service Implementation.
    - [ ] Ensure the gRPC server in `main.go` (now in `server/`) correctly instantiates the new Agent and Workflow components.
- [ ] Task: Verify with Unit Tests.
    - [ ] Update existing tests to match new package names.
    - [ ] Confirm all tests pass (`go test ./...`).
- [ ] Task: Conductor - User Manual Verification 'Component Role Clarification' (Protocol in workflow.md)

## Phase 3: Final Integration and Path Verification

### Objective
Ensure end-to-end functionality is preserved and paths are correctly configured.

### Tasks
- [ ] Task: Update Sandbox and Integration Tests.
    - [ ] Update `test/sandbox` paths in the Go integration tests.
    - [ ] Verify `SANDBOX_TEST=1 go test ./...` passes.
- [ ] Task: Final Manual Verification in VS Code.
    - [ ] Launch the VS Code extension from its new location in `source/clients/vscode/`.
    - [ ] Verify the "@cercano" chat participant still works and communicates with the backend.
- [ ] Task: Conductor - User Manual Verification 'Final Integration and Path Verification' (Protocol in workflow.md)
