# Advanced IDE Integration & Smart Escalation

## Overview
This feature elevated the Cercano IDE extension from a simple chat interface into a proactive coding assistant. It added a "safe apply" workflow so generated code is reviewed in a diff view before being written to disk, richer response rendering and progress feedback, and "smart escalation" logic that routes requests between the local model and a cloud provider — explicitly on request, automatically as a fallback after repeated local failures, and proactively for high-complexity tasks. The cloud provider was wired against a `CloudModelProvider` interface (Mock implementation) in preparation for real cloud APIs in a later track.

## Design / Architecture
- **Structured responses (proto)** — `agent.proto` gained a `FileChange` message (path, content, action) and `ProcessRequestResponse` carries an optional list of `FileChange` plus `RoutingMetadata` (which model was used, confidence). gRPC stubs were regenerated for Go (server) and TypeScript (VS Code extension); internal response structures were refactored to carry the new `FileChange` type.
- **Smart escalation in the backend** — Router classification was enhanced to detect high-complexity prompts and route them straight to the `CloudModelProvider`. The `GenerationCoordinator` tracks attempts in the self-correction loop and automatically escalates to the cloud provider after `N` local failures (configurable, default 2). The Agent detects explicit keywords like "use cloud" and overrides routing.
- **VS Code safe apply** — The extension translates `FileChange` messages into native `WorkspaceEdit` objects and calls `vscode.workspace.applyEdit` with the metadata flag to surface VS Code's Refactor Preview (diff) UI, so users review and approve before changes hit disk.
- **Rich rendering & progress** — Chat responses render full markdown (tables, lists, syntax-highlighted code blocks). `response.progress()` reports specific states: "Routing...", "Generating (Local)...", "Validating...", "Escalating to Cloud...".

## Key Behaviors / Capabilities
- Ask "create unit tests" → see a diff view of the proposed file → click Apply to save.
- Say "use cloud for this" → backend routes to the cloud provider (Mock) and reports it.
- Local model failing to fix compilation errors twice → the 3rd attempt is handled by the cloud provider (Mock).
- Full end-to-end flow: request → local attempt → self-correction → cloud fallback → Refactor Preview → Apply.

## Notable Decisions / Constraints
- Cloud routing targets a `CloudModelProvider` interface with a Mock implementation; real cloud APIs (OpenAI/Anthropic), API-key management, and secure storage were out of scope (subsequent track).
- Automatic fallback threshold is configurable, default 2 local attempts.
- Zed-specific UI implementation was out of scope for this track.
