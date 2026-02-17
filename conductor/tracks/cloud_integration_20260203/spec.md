# Track Specification: Real Cloud Model Integration

## 1. Job Title
Implement model-agnostic cloud integration for the Go backend.

## 2. Overview
This track focuses on replacing the current "Mock Cloud Model" with real, model-agnostic cloud integrations. The implementation will leverage `langchaingo` to support multiple providers (starting with Google Gemini and Anthropic) and will implement a session-based security model where API keys are managed by the IDE client.

## 3. Requirements

### 3.1 Model-Agnostic Abstraction
*   **LangChainGo Integration:** The backend MUST use the `langchaingo` library to abstract LLM provider implementations.
*   **Initial Providers:** Implement and verify integrations for **Google Gemini** and **Anthropic Claude**.
*   **Factory Pattern:** The backend MUST implement a factory or strategy pattern to instantiate the correct provider based on configuration sent by the client.

### 3.2 Key & Session Management
*   **Stateless gRPC:** The `ProcessRequest` gRPC call MUST be updated to accept provider configuration and authentication credentials (API Keys) optionally.
*   **IDE Secret Storage:** The VS Code extension MUST use `vscode.SecretStorage` to securely store API keys.
*   **Session Initiation:** Implement a mechanism to pass these credentials from the IDE to the Go backend for each session or request.

### 3.3 Functional Features
*   **Switching Providers:** Users MUST be able to switch between local models and specific cloud providers via the IDE interface (e.g., settings or chat commands).
*   **Fallthrough Support:** The backend MUST correctly route "Cloud" requests to the newly implemented real providers rather than the mock.

## 4. Architecture Impact
*   **Proto Updates:** Update `agent.proto` to include a `ProviderConfig` message (type, model name, credentials).
*   **Provider Interface:** Replace the `MockProvider` with a `CloudProvider` that wraps `langchaingo`.
*   **Client UI:** Add VS Code commands or settings to manage API keys.

## 5. Acceptance Criteria
*   [ ] The Go backend can successfully process a request using a real Google Gemini API key.
*   [ ] The Go backend can successfully process a request using a real Anthropic API key.
*   [ ] API keys are NEVER stored in the backend filesystem or logs; they are provided by the client.
*   [ ] The VS Code extension provides a way to input and store keys for both Gemini and Anthropic.

## 6. Out of Scope
*   Advanced LLM features like streaming (to be handled in a later track).
*   Vector database or long-term memory implementation.
