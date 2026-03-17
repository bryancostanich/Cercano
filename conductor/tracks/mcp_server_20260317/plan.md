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

## Phase 2: Core Tools — Generate & Chat

### Objective
Implement the two primary tools that map directly to existing gRPC RPCs.

### Tasks
- [ ] Task: Implement `cercano_chat` tool.
    - [ ] Define input schema (message, context, conversation_id).
    - [ ] Map to `ProcessRequest` gRPC call (no workDir/fileName).
    - [ ] Return natural language response with routing metadata.
    - [ ] Red phase: Write tests against a mock gRPC client.
    - [ ] Green phase: Implement and pass.
- [ ] Task: Implement `cercano_generate` tool.
    - [ ] Define input schema (instruction, file_path, work_dir, context, conversation_id).
    - [ ] Map to `ProcessRequest` gRPC call with workDir and fileName.
    - [ ] Return generated code, file changes, validation errors, routing metadata.
    - [ ] Red phase: Write tests against a mock gRPC client.
    - [ ] Green phase: Implement and pass.
- [ ] Task: Test both tools end-to-end with a running Cercano gRPC server.
- [ ] Task: Conductor - User Manual Verification 'Core Tools — Generate & Chat' (Protocol in workflow.md)

## Phase 3: Utility Tools — Review, Summarize, Classify

### Objective
Add higher-level tools that wrap the core gRPC API with prompt templates and SmartRouter access.

### Tasks
- [ ] Task: Implement `cercano_review` tool.
    - [ ] Define input schema (code, instructions, file_path).
    - [ ] Create review prompt template in `internal/mcp/prompts.go`.
    - [ ] Map to `ProcessRequest` gRPC call with templated prompt.
    - [ ] Red/Green TDD.
- [ ] Task: Implement `cercano_summarize` tool.
    - [ ] Define input schema (content, format).
    - [ ] Create summarization prompt template.
    - [ ] Map to `ProcessRequest` gRPC call with templated prompt.
    - [ ] Red/Green TDD.
- [ ] Task: Implement `cercano_classify` tool.
    - [ ] Define input schema (query).
    - [ ] Determine approach: either add a lightweight `Classify` RPC to the gRPC server, or have the MCP server call `ProcessRequest` with a classification prompt and parse the result.
    - [ ] Return intent (coding/chat), recommended provider (local/cloud), confidence.
    - [ ] Red/Green TDD.
- [ ] Task: Test all utility tools end-to-end.
- [ ] Task: Conductor - User Manual Verification 'Utility Tools — Review, Summarize, Classify' (Protocol in workflow.md)

## Phase 4: Configuration Tool & Multi-Turn Support

### Objective
Add runtime configuration management and verify multi-turn conversation support across MCP tool calls.

### Tasks
- [ ] Task: Implement `cercano_config` tool.
    - [ ] Define input schema (action, local_model, cloud_provider, cloud_model).
    - [ ] "set" action maps to `UpdateConfig` gRPC call.
    - [ ] "get" action returns current config (may require a new RPC or cached state).
    - [ ] Red/Green TDD.
- [ ] Task: Verify multi-turn conversations work across sequential MCP tool calls.
    - [ ] Test: call `cercano_chat` with conversation_id, follow up with second call using same ID.
    - [ ] Verify the second response reflects context from the first turn.
- [ ] Task: Conductor - User Manual Verification 'Configuration Tool & Multi-Turn Support' (Protocol in workflow.md)

## Phase 5: Integration & Agent Testing

### Objective
Validate the MCP server works with real agents (Claude Code), add build/install scripts, and clean up.

### Tasks
- [ ] Task: Add MCP server build target to the project's build system.
    - [ ] `go build -o bin/cercano-mcp cmd/mcp/main.go`
- [ ] Task: Write a Claude Code MCP configuration example for connecting to Cercano.
    - [ ] Document the `.claude.json` or `mcp_servers` config entry.
- [ ] Task: Test with Claude Code — verify tool discovery, `cercano_chat`, and `cercano_generate` work.
- [ ] Task: Test with at least one other MCP-compatible agent (e.g., Cursor) if available.
- [ ] Task: Add error handling for common failure modes:
    - [ ] gRPC server not running.
    - [ ] Ollama not running.
    - [ ] Invalid model name.
- [ ] Task: Update README.md with MCP server documentation (setup, usage, tool reference).
- [ ] Task: Run full test suite — `go test ./...`.
- [ ] Task: Conductor - User Manual Verification 'Integration & Agent Testing' (Protocol in workflow.md)
