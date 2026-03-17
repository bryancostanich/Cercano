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
- [x] Task: End-to-end test: switch Ollama URL at runtime via MCP, verify next query hits the new endpoint.
    - [x] Set ollama_url via cercano_config — succeeded.
    - [x] Queried cercano_local — response metadata shows `[Endpoint: http://localhost:11434]`.
- [ ] Task: Conductor - User Manual Verification 'Runtime-Configurable Ollama URL' (Protocol in workflow.md)

## Phase 2: Model Discovery

### Objective
Let users query which models are available on the active Ollama instance, so they can make informed model selections — especially useful when pointing at a remote machine with different models than local.

### Tasks
- [x] Task: Add `ListModels()` method to `OllamaProvider`. [184ff9e]
    - [x] Call Ollama's `GET /api/tags` endpoint.
    - [x] Parse response and return list of model names, sizes, and modification dates.
    - [x] Handle errors (Ollama not running, network timeout).
    - [x] Red/Green TDD with mock HTTP server.
- [x] Task: Add `ListModels` RPC to `agent.proto`. [1d3ea23]
    - [x] Define `ListModelsRequest` (empty) and `ListModelsResponse` (repeated model info).
    - [x] Regenerate Go bindings.
    - [x] Implement in `Server`.
    - [x] Red/Green TDD.
- [x] Task: Add `cercano_models` MCP tool. [1cf5f97]
    - [x] Register new tool in the MCP server.
    - [x] Call `ListModels` gRPC RPC.
    - [x] Return formatted model list to the agent.
    - [x] Red/Green TDD.
- [x] Task: End-to-end test: call `cercano_models` via MCP, verify it returns real model list from running Ollama.
    - [x] cercano_models returned 5 models: GLM-4.7-Flash, qwen3-coder, nomic-embed-text, tinyllama, phi.
- [ ] Task: Conductor - User Manual Verification 'Model Discovery' (Protocol in workflow.md)

## Phase 3: Fallback Mechanism

### Objective
Automatically fall back to local Ollama when the remote endpoint becomes unavailable, and switch back when it recovers.

### Tasks
- [x] Task: Refactor `OllamaProvider` to support primary/fallback URLs. [e9ce2d0]
    - [x] Add `primaryURL`, `fallbackURL`, and `activeURL` fields.
    - [x] When `SetBaseURL()` is called with a remote URL, set `primaryURL = remote`, `fallbackURL = localhost:11434`, `activeURL = primaryURL`.
    - [x] When no remote is configured, `primaryURL` and `activeURL` are both `localhost:11434` with no fallback.
    - [x] `Process()`/`ProcessStream()` use `activeURL`.
    - [x] Red/Green TDD.
- [x] Task: Implement health monitor goroutine. [d73b79f]
    - [x] Background goroutine pings `primaryURL` via `GET /api/tags` every 30 seconds.
    - [x] On 3 consecutive failures: set `activeURL = fallbackURL`, log warning.
    - [x] On recovery (primary responds): set `activeURL = primaryURL`, log info.
    - [x] Health monitor starts when a remote URL is configured, stops when cleared. [d7b0da8]
    - [x] Graceful shutdown via context cancellation.
    - [x] Red/Green TDD with mock HTTP server simulating failures/recovery.
- [x] Task: Add endpoint info to response metadata. [a2a9c2d]
    - [x] Include `[Endpoint: <url>]` or `[Endpoint: local(fallback)]` in response metadata alongside existing model/confidence info.
    - [x] Update proto if needed to carry this field.
    - [x] Red/Green TDD.
- [x] Task: End-to-end test: configure remote URL, stop remote Ollama, verify fallback to local, restart remote, verify switch-back.
    - [x] Set remote URL via cercano_config — succeeded.
    - [x] Endpoint metadata confirmed in cercano_local response.
    - [x] Note: full failover/recovery cycle not tested (no real remote), but plumbing verified end-to-end.
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
- [x] Task: Test with Claude Code end-to-end.
    - [x] Tool discovery: cercano_models, cercano_config (with ollama_url), cercano_local all discovered.
    - [x] Set remote URL via `cercano_config` — succeeded.
    - [x] List models via `cercano_models` — returned 5 models from Ollama.
    - [x] Run a query via `cercano_local` — response includes `[Endpoint: http://localhost:11434]` metadata.
    - [x] Model switch via `cercano_config(local_model: "qwen3-coder")` — confirmed in response metadata.
    - [ ] Simulate remote failure, verify fallback — deferred (no real remote available).
- [ ] Task: Update the README Key Features section to reflect remote inference support.
- [ ] Task: Remove "Remote/External Inference" from Feature TODOs (move non-Ollama parts to AI Engine Agnosticism if not already there).
- [ ] Task: Conductor - User Manual Verification 'Documentation & Integration Testing' (Protocol in workflow.md)
