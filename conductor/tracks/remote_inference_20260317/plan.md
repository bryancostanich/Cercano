# Track Plan: Remote Inference

## Phase 1: Runtime-Configurable Ollama URL

### Objective
Make the Ollama endpoint URL changeable at runtime via gRPC and MCP, with thread-safe access in `OllamaProvider`.

### Tasks
- [x] Task: Make `BaseURL` thread-safe in `OllamaProvider`. [35defa8]
    - [x] Add `SetBaseURL(url string)` method protected by the existing `sync.RWMutex`.
    - [x] Update `Process()` and `ProcessStream()` to read `BaseURL` under read lock (currently only `ModelName` is locked).
    - [x] Red/Green TDD: test concurrent `SetBaseURL` + `Process` calls.
- [x] Task: Add `ollama_url` field to `UpdateConfigRequest` in `agent.proto`. [4cbf2f7]
    - [x] Regenerate Go bindings (`protoc`).
    - [x] Update `Server.UpdateConfig()` to call `OllamaProvider.SetBaseURL()`.
    - [x] Validate URL format (must be valid HTTP/HTTPS URL).
    - [x] Red/Green TDD.
- [x] Task: Update `cercano_config` MCP tool to support `ollama_url` parameter. [96e72b2]
    - [x] Add `ollama_url` to the tool's input schema.
    - [x] Pass through to `UpdateConfig` gRPC call.
    - [x] Red/Green TDD.
- [ ] Task: End-to-end test: switch Ollama URL at runtime via MCP, verify next query hits the new endpoint.
- [ ] Task: Conductor - User Manual Verification 'Runtime-Configurable Ollama URL' (Protocol in workflow.md)

## Phase 2: Model Discovery

### Objective
Let users query which models are available on the active Ollama instance, so they can make informed model selections — especially useful when pointing at a remote machine with different models than local.

### Tasks
- [ ] Task: Add `ListModels()` method to `OllamaProvider`.
    - [ ] Call Ollama's `GET /api/tags` endpoint.
    - [ ] Parse response and return list of model names, sizes, and modification dates.
    - [ ] Handle errors (Ollama not running, network timeout).
    - [ ] Red/Green TDD with mock HTTP server.
- [ ] Task: Add `ListModels` RPC to `agent.proto`.
    - [ ] Define `ListModelsRequest` (empty) and `ListModelsResponse` (repeated model info).
    - [ ] Regenerate Go bindings.
    - [ ] Implement in `Server`.
    - [ ] Red/Green TDD.
- [ ] Task: Add `cercano_models` MCP tool.
    - [ ] Register new tool in the MCP server.
    - [ ] Call `ListModels` gRPC RPC.
    - [ ] Return formatted model list to the agent.
    - [ ] Red/Green TDD.
- [ ] Task: End-to-end test: call `cercano_models` via MCP, verify it returns real model list from running Ollama.
- [ ] Task: Conductor - User Manual Verification 'Model Discovery' (Protocol in workflow.md)

## Phase 3: Fallback Mechanism

### Objective
Automatically fall back to local Ollama when the remote endpoint becomes unavailable, and switch back when it recovers.

### Tasks
- [ ] Task: Refactor `OllamaProvider` to support primary/fallback URLs.
    - [ ] Add `primaryURL`, `fallbackURL`, and `activeURL` fields.
    - [ ] When `SetBaseURL()` is called with a remote URL, set `primaryURL = remote`, `fallbackURL = localhost:11434`, `activeURL = primaryURL`.
    - [ ] When no remote is configured, `primaryURL` and `activeURL` are both `localhost:11434` with no fallback.
    - [ ] `Process()`/`ProcessStream()` use `activeURL`.
    - [ ] Red/Green TDD.
- [ ] Task: Implement health monitor goroutine.
    - [ ] Background goroutine pings `primaryURL` via `GET /api/tags` every 30 seconds.
    - [ ] On 3 consecutive failures: set `activeURL = fallbackURL`, log warning.
    - [ ] On recovery (primary responds): set `activeURL = primaryURL`, log info.
    - [ ] Health monitor starts when a remote URL is configured, stops when cleared.
    - [ ] Graceful shutdown via context cancellation.
    - [ ] Red/Green TDD with mock HTTP server simulating failures/recovery.
- [ ] Task: Add endpoint info to response metadata.
    - [ ] Include `[Endpoint: <url>]` or `[Endpoint: local(fallback)]` in response metadata alongside existing model/confidence info.
    - [ ] Update proto if needed to carry this field.
    - [ ] Red/Green TDD.
- [ ] Task: End-to-end test: configure remote URL, stop remote Ollama, verify fallback to local, restart remote, verify switch-back.
- [ ] Task: Conductor - User Manual Verification 'Fallback Mechanism' (Protocol in workflow.md)

## Phase 4: Documentation & Integration Testing

### Objective
Update documentation, test with real agents, and verify the complete remote inference workflow.

### Tasks
- [ ] Task: Update README.md.
    - [ ] Add "Remote Inference" section explaining setup (point at a LAN machine running Ollama).
    - [ ] Document `cercano_config` usage for setting remote URL.
    - [ ] Document `cercano_models` tool.
    - [ ] Document fallback behavior.
- [ ] Task: Test with Claude Code end-to-end.
    - [ ] Set remote URL via `cercano_config`.
    - [ ] List models via `cercano_models`.
    - [ ] Run a query, verify it hits the remote endpoint.
    - [ ] Simulate remote failure, verify fallback.
- [ ] Task: Update the README Key Features section to reflect remote inference support.
- [ ] Task: Remove "Remote/External Inference" from Feature TODOs (move non-Ollama parts to AI Engine Agnosticism if not already there).
- [ ] Task: Conductor - User Manual Verification 'Documentation & Integration Testing' (Protocol in workflow.md)
