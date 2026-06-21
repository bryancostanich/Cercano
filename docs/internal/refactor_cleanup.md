# Project Architecture Refactor and Cleanup

**Status:** Shipped maintenance.
**Type:** chore.

## Overview

Reorganized the codebase to resolve structural confusion: established a clear server/client directory hierarchy, unified all code under `source/`, and redefined component boundaries (Router vs. Coordinator) to reflect their actual roles in the agentic system.

## What was done

### Phase 1 — Directory restructuring (checkpoint bf07bcd)

- Created `source/server/` and `source/clients/`.
- Moved IDE extensions: `vscode-extension/` → `source/clients/vscode/`, `zed-extension/` → `source/clients/zed/`.
- Moved core Go code (everything except `proto/` and `clients/`) into `source/server/`.
- Updated the Go module and import paths; ran `go fmt`.
- Updated the VS Code extension config (`package.json`, `tsconfig.json`, `launch.json`); verified `npm run compile`.

### Phase 2 — Component role clarification (checkpoint a1d8f23)

- Formalized the **Agent** domain (`source/server/internal/agent/`) as the primary logic brain for intent classification and decision-making — folding in the former Router logic.
- Defined the **Workflows** domain (`source/server/internal/workflows/`) as the executor layer; moved `GenerationCoordinator` there. The Coordinator is now a specific workflow executor (e.g. the test-generation loop) invoked by the Agent.
- Updated the gRPC server in `main.go` to instantiate the new Agent and Workflow components.
- Updated and passed unit tests against the new package names.

### Phase 3 — Final integration and path verification (checkpoint 4bd450c)

- Updated `test/sandbox` paths in the Go integration tests.
- Verified `SANDBOX_TEST=1 go test ./...` and end-to-end VS Code functionality.

## Outcome vs. acceptance criteria

- `source/` now contains `server/`, `clients/`, and `proto/`.
- All functional features (unit test generation, self-correction loop) preserved.
- Router/Agent/Coordinator ambiguity eliminated; structure accommodates new clients (CLI/Web) and server capabilities.
- VS Code extension connects to the backend and generates tests end-to-end.

## Out of scope

- New agent/router features.
- Full Zed extension implementation (only the existing scaffold was moved).
