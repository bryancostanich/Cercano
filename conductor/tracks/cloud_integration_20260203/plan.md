# Track Plan: Real Cloud Model Integration

This plan outlines the steps to integrate real cloud LLM providers into the Cercano backend using `langchaingo` and secure client-side key management.

## Phase 1: Protocol & Authentication Setup [checkpoint: efc7e58]

### Objective
Update the gRPC contract to support passing provider configuration and credentials.

### Tasks
- [x] Task: Update `agent.proto`. [5f31211]
    - [x] Add `CloudProviderConfig` message (provider type, model, api_key).
    - [x] Update `ProcessRequestRequest` to include an optional `CloudProviderConfig`.
- [x] Task: Re-generate gRPC stubs (Go & TypeScript). [5f31211]
- [x] Task: Implement VS Code Secret Management. [5d544d5]
    - [x] Add commands to set/get Gemini and Anthropic keys using `vscode.SecretStorage`.
    - [x] Update the Chat Participant to retrieve these keys and include them in the gRPC request.
- [x] Task: Conductor - User Manual Verification 'Protocol & Authentication Setup' (Protocol in workflow.md) [cc8b2c8]

## Phase 2: Backend Provider Abstraction (LangChainGo)

### Objective
Implement the server-side logic to dynamically instantiate cloud providers.

### Tasks
- [x] Task: Integrate `langchaingo`. [813728f]
    - [x] Add `github.com/tmc/langchaingo` to `go.mod`.
- [x] Task: Implement `langchaingo` Wrapper. [aabb6a1]
    - [x] Create a `CloudModelProvider` struct that wraps `langchaingo`'s `llms.Model` interface.
    - [x] Implement a factory function to create providers (Gemini, Anthropic) based on the gRPC config.
- [x] Task: Update Agent Logic. [7cdf1a6]
    - [x] Update the Router to use the new `CloudModelProvider` when routing to cloud.
- [ ] Task: Verify with Integration Tests.
    - [ ] Create a test harness that uses environment variables for real API keys (to be run manually).
- [ ] Task: Conductor - User Manual Verification 'Backend Provider Abstraction' (Protocol in workflow.md)

## Phase 3: End-to-End Verification

### Objective
Verify that the full loop from VS Code to Cloud LLM works with real keys.

### Tasks
- [ ] Task: Full Loop Test - Google Gemini.
    - [ ] Configure Gemini key in VS Code.
    - [ ] Send request and verify response from Gemini.
- [ ] Task: Full Loop Test - Anthropic Claude.
    - [ ] Configure Anthropic key in VS Code.
    - [ ] Send request and verify response from Claude.
- [ ] Task: Conductor - User Manual Verification 'End-to-End Verification' (Protocol in workflow.md)
