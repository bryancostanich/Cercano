# Track Plan: VS Code IDE Integration Fixes

## Phase 1: Bug Fixes

### Objective
Fix all identified bugs in extension.ts so the code review/apply workflow functions correctly.

### Tasks
- [x] Task: Fix Bug 2 — Remove duplicate participant registration. [8dc94cb]
- [x] Task: Fix Bug 4 — Resolve followup command/prompt ambiguity. [8dc94cb]
    - Note: ChatFollowup.command is a participant command, not a VS Code command. Switched to response.button() which correctly accepts VS Code commands with arguments.
- [x] Task: Fix Bug 1 — Thread filePaths through followup to command handlers. [8dc94cb]
    - Note: response.button() is the correct API for this. followupProvider removed for Apply/Preview/Reject.
- [x] Task: Fix Bug 3 — Replace hardcoded WorkspaceEdit range. [8dc94cb]
- [ ] Task: Conductor - User Manual Verification 'Bug Fixes' (Protocol in workflow.md)

## Phase 2: Test Coverage & End-to-End Verification

### Objective
Ensure the fixed workflow is covered by tests and verified manually end-to-end.

### Tasks
- [x] Task: Review and update existing client tests (`test/client.test.ts`) for regressions.
- [x] Task: Add tests for the followup command argument threading (Bug 1 fix).
- [x] Task: Manual end-to-end verification:
    - [x] Start the Go backend server.
    - [x] Open the VS Code extension in Extension Development Host.
    - [x] Send a coding request; confirm file tree appears.
    - [x] Confirm Apply Changes writes file content correctly.
    - [x] Known limitation: buttons persist after use (VS Code Chat API limitation; accepted).
- [x] Task: Conductor - User Manual Verification 'End-to-End Verification' (Protocol in workflow.md)
