# Track Plan: Advanced IDE Integration & Smart Escalation Logic

This plan outlines the phases and tasks required to transition Cercano into a proactive assistant with structured file edits and smart model escalation.

## Phase 1: Protocol & Data Model Foundation [checkpoint: a564092]

### Objective
Update the communication contract between Client and Server to support structured file changes and explicit routing signals.

### Tasks
- [x] Task: Update `agent.proto`.
    - [x] Add `FileChange` message (path, content, action).
    - [x] Update `ProcessRequestResponse` to include an optional list of `FileChange`.
    - [x] Add `RoutingMetadata` to the response (which model was used, confidence).
- [x] Task: Re-generate gRPC stubs.
    - [x] Generate Go stubs for the server.
    - [x] Generate TypeScript stubs for the VS Code extension.
- [x] Task: Update Backend Domain Models.
    - [x] Refactor internal response structures to support the new `FileChange` type.
- [x] Task: Conductor - User Manual Verification 'Protocol & Data Model Foundation' (Protocol in workflow.md)

## Phase 2: Smart Escalation & Routing Logic [checkpoint: 2d59b99]

### Objective
Implement the "intelligence" that decides when to use the Local Model vs. the Cloud Model (Mock).

### Tasks
- [x] Task: Enhance Router Classification.
    - [x] Red phase: Write tests for "High Complexity" prompt detection.
    - [x] Green phase: Update Router prototypes and threshold logic to escalate complex tasks.
- [x] Task: Implement Automatic Fallback in Coordinator.
    - [x] Red phase: Write tests for the `GenerationCoordinator` where it fails twice locally and succeeds on the 3rd attempt via Cloud.
    - [x] Green phase: Update `Coordinate` method to track attempts and switch providers upon threshold breach.
- [x] Task: Handle Explicit 'Cloud' Requests.
    - [x] Update Agent logic to detect keywords like "use cloud" and override routing.
- [x] Task: Conductor - User Manual Verification 'Smart Escalation & Routing Logic' (Protocol in workflow.md)

## Phase 3: VS Code 'Safe Apply' Integration [checkpoint: ff89e1a]

### Objective
Implement the user-facing workflow for reviewing and applying code changes using VS Code's native APIs.

### Tasks
- [x] Task: Implement `WorkspaceEdit` Handler in Extension.
    - [x] Create logic to translate `FileChange` messages into VS Code `WorkspaceEdit` objects.
- [x] Task: Implement Refactor Preview Trigger.
    - [x] Update the Chat Participant handler to call `vscode.workspace.applyEdit` with the `metadata` flag to show the diff UI.
- [x] Task: Verify 'Safe Apply' Workflow.
    - [x] Manually verify that generating a unit test opens the "Refactor Preview" panel rather than just printing text.
- [x] Task: Conductor - User Manual Verification 'VS Code Safe Apply Integration' (Protocol in workflow.md)

## Phase 4: UI Polish & Feedback

### Objective
Improve the visual feedback and rendering of the assistant's progress and results.

### Tasks
- [~] Task: Enhance Markdown Rendering.
    - [ ] Verify that code blocks returned by the backend are correctly highlighted in the native Chat window.
- [ ] Task: Implement Detailed Progress Reporting.
    - [ ] Use `response.progress()` to show specific states: "Routing...", "Generating (Local)...", "Validating...", "Escalating to Cloud...".
- [ ] Task: Final End-to-End System Test.
    - [ ] Verify the full flow: User Request -> Local Attempt -> Self-Correction -> Fallback to Cloud -> Refactor Preview -> Apply.
- [ ] Task: Conductor - User Manual Verification 'UI Polish & Feedback' (Protocol in workflow.md)
