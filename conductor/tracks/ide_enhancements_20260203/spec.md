# Track Specification: Advanced IDE Integration & Smart Escalation Logic

## 1. Job Title
Improve the experience of Cercano in the IDE with a more full-featured integration.

## 2. Overview
This track focuses on elevating the IDE extension from a simple chat interface to a proactive coding assistant. It aims to implement "safe apply" workflows for generated code, enhance the visual rendering of responses, and implement the logic for "Smart Escalation" (transitioning request routing), while preparing the architecture for real cloud providers in a future track.

## 3. Requirements

### 3.1 Actionable Output (File Modification)
*   **Structured Responses:** The gRPC backend MUST return code generation results in a structured format (e.g., `FileChange` protobuf message) rather than just raw markdown text.
*   **VS Code Integration:** The VS Code extension MUST interpret these `FileChange` messages and use the native `WorkspaceEdit` API to present changes.
*   **Refactor Preview:** Users MUST be presented with a "Refactor Preview" (diff view) to review and approve changes before they are written to disk.

### 3.2 Smart Escalation Logic (Wiring)
*   **Explicit Trigger:** The user MUST be able to explicitly request cloud processing (e.g., "use cloud") via chat. The backend must route this to the `CloudModelProvider` interface (currently Mock).
*   **Automatic Fallback:** The **Self-Correction Loop** (in the backend) MUST automatically escalate to the `CloudModelProvider` interface if the Local Model fails to produce valid code after `N` attempts (configurable, default 2).
*   **Complexity Routing:** Improve the Router's initial classification to better identify "High Complexity" tasks and route them to the `CloudModelProvider` interface immediately.

### 3.3 Rich Rendering
*   **Markdown Support:** Ensure all chat responses support rich markdown (tables, lists, code blocks with syntax highlighting).
*   **Progress Indication:** Improve visibility of the "Thinking..." and "Self-Correcting..." states in the UI.

## 4. Architecture Impact
*   **Proto Updates:** Update `agent.proto` to support structured `FileChange` responses.
*   **Backend Logic:** Update `GenerationCoordinator` to implement the fallback/escalation logic.
*   **Client Logic:** Update VS Code `extension.ts` to handle `WorkspaceEdit`.

## 5. Acceptance Criteria
*   [ ] User can ask "create unit tests", see a Diff View of the proposed file, and click "Apply" to save it.
*   [ ] User can say "use cloud for this" and the backend logs/returns that it is using the "Cloud Provider" (Mock).
*   [ ] If the local model fails to fix compilation errors 2 times in a row, the 3rd attempt is handled by the "Cloud Provider" (Mock).
*   [ ] Chat interface renders code blocks correctly.

## 6. Out of Scope
*   Integration with real cloud APIs (OpenAI/Anthropic). This will be a subsequent track.
*   API Key management and secure storage.
*   Zed specific UI implementation.
