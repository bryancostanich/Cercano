# Real Cloud Model Integration

## Overview

The placeholder "Mock Cloud Model" was replaced with real, model-agnostic cloud LLM integration in the Go backend. Built on `langchaingo`, the backend supports multiple providers — initially Google Gemini and Anthropic Claude — and uses a session-based security model where API keys live in the IDE client and are never persisted server-side.

## Design / Architecture

- **Provider abstraction** — A `CloudModelProvider` struct wraps `langchaingo`'s `llms.Model` interface, replacing the prior `MockProvider`. A factory/strategy function instantiates the correct provider (Gemini or Anthropic) from configuration supplied by the client.
- **Routing** — The `SmartRouter` routes "cloud" requests to the real `CloudModelProvider` instead of the mock.
- **Protocol** — `agent.proto` gained a `CloudProviderConfig` message (provider type, model name, API key); `ProcessRequestRequest` carried it as optional config. Go and TypeScript stubs were regenerated. (Note: a later track moved cloud config out of the per-request message into a dedicated `UpdateConfig` RPC.)
- **Key management** — The VS Code extension stores Gemini and Anthropic keys in `vscode.SecretStorage`, exposes commands to set/get them, and the chat participant attaches them to the gRPC request. Keys are never written to the backend filesystem or logs.

## Key Behaviors / Capabilities

- Backend processes requests against a real Google Gemini key.
- Backend processes requests against a real Anthropic Claude key.
- Users switch between local models and specific cloud providers via the IDE.
- VS Code provides UI/commands to input and securely store keys for both providers.
- An integration test harness reads real API keys from environment variables for manual end-to-end runs.

## Notable Decisions / Constraints

- **Model-agnostic via `langchaingo`** — provider implementations are abstracted behind the library so additional providers can be added through the factory.
- **Client-owned credentials** — API keys are provided per session/request by the IDE; the backend stays stateless with respect to secrets and never stores or logs them.
- Out of scope for this track: streaming responses, and vector database / long-term memory.
