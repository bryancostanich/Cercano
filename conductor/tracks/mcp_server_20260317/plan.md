# Track Plan: Cercano as MCP Server

## Phase 1: MCP SDK & Package Setup

### Objective
Add the MCP SDK dependency, establish the package structure, and implement a minimal MCP server that starts and responds to the initialize handshake.

### Tasks
- [x] Task: Evaluate and select Go MCP SDK. Selected `modelcontextprotocol/go-sdk` v1.4.1 (official SDK, stable v1.x, maintained by MCP org + Google). Added to `go.mod`.
- [x] Task: Create `internal/mcp/` package with `server.go` skeleton.
- [x] Task: Create `cmd/mcp/main.go` entry point that starts the MCP server on stdio.
    - [x] Accept `--grpc-addr` flag (default: `localhost:50052`).
    - [x] Initialize gRPC client connection to Cercano server.
    - [x] Start MCP server with stdio transport.
- [x] Task: Verify the MCP server starts, responds to `initialize`, and advertises an empty tool list.
- [ ] Task: Conductor - User Manual Verification 'MCP SDK & Package Setup' (Protocol in workflow.md)

## Phase 2: Core Tool — `cercano_local`

### Objective
Implement a single flexible tool that runs any prompt against local models via the existing gRPC API. The SmartRouter handles intent classification internally. Specialized tools (review, summarize, etc.) are deferred to the Agent Skills & Tool Use track, which will evaluate whether they improve agent ergonomics.

### Tasks
- [x] Task: Implement `cercano_local` tool.
    - [x] Define input schema (prompt, file_path, work_dir, context, conversation_id).
    - [x] Map to `ProcessRequest` gRPC call. If work_dir and file_path are provided, the SmartRouter routes to the coding path (agentic generate-validate loop). Otherwise, it handles as a direct LLM call.
    - [x] Return output text, file changes (if any), validation errors (if any), routing metadata.
    - [x] Red phase: Write tests against a mock gRPC client.
    - [x] Green phase: Implement and pass.
- [x] Task: Test end-to-end with a running Cercano gRPC server (both chat-style and code generation queries).
- [ ] Task: Conductor - User Manual Verification 'Core Tool — cercano_local' (Protocol in workflow.md)

## Phase 3: Configuration Tool & Multi-Turn Support

### Objective
Add runtime configuration management and verify multi-turn conversation support across MCP tool calls.

### Tasks
- [x] Task: Implement `cercano_config` tool.
    - [x] Define input schema (action, local_model, cloud_provider, cloud_model).
    - [x] "set" action maps to `UpdateConfig` gRPC call.
    - [x] "get" action deferred — no existing gRPC RPC for querying config. Noted for future.
    - [x] Red/Green TDD.
- [x] Task: Verify multi-turn conversations work across sequential MCP tool calls.
    - [x] Test: call `cercano_local` with conversation_id, follow up with second call using same ID.
    - [x] Verify the second call passes the same conversation_id to the gRPC server.
- [ ] Task: Conductor - User Manual Verification 'Configuration Tool & Multi-Turn Support' (Protocol in workflow.md)

## Phase 4: Integration & Agent Testing

### Objective
Validate the MCP server works with real agents (Claude Code), add build/install scripts, and clean up.

### Tasks
- [x] Task: Add MCP server build target to the project's build system.
    - [x] `go build -o bin/cercano-mcp cmd/mcp/main.go` — added Makefile with `make mcp`, `make agent`, `make all`, `make test`, `make clean`.
- [x] Task: Write a Claude Code MCP configuration example for connecting to Cercano.
    - [x] Document the `.mcp.json` config entry and `claude mcp add` CLI command.
- [x] Task: Test with Claude Code — verify tool discovery and `cercano_local` work.
    - [x] Tool discovery: both `cercano_local` and `cercano_config` discovered with correct schemas.
    - [x] Chat query: `cercano_local` returned coherent response (model: qwen3-coder).
    - [x] Config update: `cercano_config` set local_model to GLM-4.7-Flash successfully.
    - [x] Model switch verification: re-ran same prompt, confirmed new model in response metadata.
- [ ] Task: Test with at least one other MCP-compatible agent (e.g., Cursor) if available.
- [x] Task: Add error handling for common failure modes:
    - [x] gRPC server not running — actionable "connection refused" message.
    - [x] Ollama not running — actionable "ollama serve" suggestion.
    - [x] Server unavailable — clear diagnostic message.
- [x] Task: Update README.md with MCP server documentation (setup, usage, tool reference).
- [x] Task: Run full test suite — `go test ./...`.
- [ ] Task: Conductor - User Manual Verification 'Integration & Agent Testing' (Protocol in workflow.md)
