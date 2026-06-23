# Generalized Agentic Workflow

## Overview
The backend was originally specialized for a single task — generating unit tests via a hardcoded `UnitTestHandler`. This feature generalized it into a true agentic architecture that handles any coding request. It introduced an `Agent` orchestrator, replaced the unit-test handler with a domain-agnostic `GenericCodeGenerator`, generalized the validator, and added intent classification so the system dynamically chooses between an iterative coding loop and a direct chat response.

## Design / Architecture
Request flow moved from `Server -> Router -> UnitTestHandler -> LLM` to `Server -> Agent -> (Router + Intent) -> [Coordinator -> GenericGenerator -> Validator] OR [GenericGenerator direct]`.

- **`Agent` orchestrator** (`internal/agent/agent.go`) — the primary entry point for the gRPC server. `ProcessRequest(input, code)` calls the Router to select a model (local/cloud), classifies intent, and chooses the execution strategy (loop vs. direct call). `internal/server/server.go` calls the Agent instead of the Router directly.
- **`GenericCodeGenerator`** — refactored from `unittest_handler.go` into `generic_generator.go`. It accepts the user's prompt as the primary instruction (no hardcoded "write unit tests" prompt) and wraps it with an expert-Go-developer system persona. The `CodeGenerator` interface was updated to accept `instruction` and `code`.
- **`GoValidator`** — renamed from `validator.go` to `go_validator.go`, supporting both `go build` (non-test code) and `go test` (test code), chosen from the input context (e.g., filename).
- **Intent classification** — `IntentCoding` and `IntentChat` constants in the `agent` package. `ClassifyIntent` uses embedding similarity against an expanded `prototypes.yaml` to decide whether a request needs the Coordinator loop (generate/fix/refactor) or a direct answer (explain/summarize/what-is).

## Key Behaviors / Capabilities
- "Write a function to add two numbers" → detected as Coding intent, generated via the self-correction loop.
- "Explain this code" → detected as Chat intent, answered directly without the loop.
- "Write unit tests" still works (regression-preserved) via the generic path.

## Notable Decisions / Constraints
- Intent strategy selection is the core decision point: loop for coding work, direct call for chat/questions.
- Validator defaulting to `go test` vs `go build` based on filename was accepted as sufficient for this track.
- `UnitTestHandler` and all its references were removed.
- Out of scope: advanced multi-file editing and new (real) cloud integrations (handled in separate tracks).
