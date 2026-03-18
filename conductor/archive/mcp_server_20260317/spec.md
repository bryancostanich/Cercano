# Track Specification: Cercano as MCP Server

## 1. Job Title
Expose Cercano's local inference capabilities as an MCP server so cloud-based agents can delegate work to local models.

## 2. Overview
Cloud-based AI agents (Claude Code, Cursor, Copilot, etc.) burn tokens and time on tasks that could run locally for free — code summarization, simple refactoring, code review, file analysis. Cercano already has a capable local inference engine with smart routing, agentic code generation, and streaming. This track adds an MCP (Model Context Protocol) server that sits as a thin adapter in front of Cercano's existing gRPC server, exposing its capabilities as MCP tools that any MCP-compatible agent can discover and invoke.

The value proposition is simple: cloud agents offload suitable work to local inference — faster, private, and at zero cost.

**What changes:** A new `cmd/mcp/` entry point and `internal/mcp/` package are added. The MCP server connects to the existing Cercano gRPC server as a client.

**What does NOT change:** The gRPC server, agent orchestrator, SmartRouter, coordinator, providers, or IDE extensions. The MCP server is a pure consumer of the existing gRPC API.

## 3. Architecture Decision

The MCP server is implemented as a **separate process** that connects to the Cercano gRPC server as a client. This keeps the architecture clean:

```
Cloud Agent (Claude Code, Cursor, etc.)
    │
    │ MCP (stdio or SSE)
    │
┌───┴───────────────────────────┐
│       Cercano MCP Server      │
│   (cmd/mcp/ — thin adapter)   │
│                               │
│   Translates MCP tool calls   │
│   into gRPC requests          │
└───────────┬───────────────────┘
            │ gRPC
┌───────────┴───────────────────┐
│     Cercano gRPC Server       │
│   (existing, unchanged)       │
│                               │
│   Agent → Router → Provider   │
│   Coordinator → Validator     │
└───────────────────────────────┘
```

Why a separate process instead of embedding MCP into the gRPC server:
- **Separation of concerns** — MCP transport logic doesn't pollute the core server.
- **Independent lifecycle** — MCP server can start/stop without affecting IDE clients.
- **Multiple transports** — stdio for CLI agents (Claude Code), SSE for browser-based agents, without complicating the core.
- **Simpler testing** — MCP adapter can be tested against a mock gRPC client.

## 4. MCP Tools

The MCP server exposes the following tools:

### 4.1 `cercano_generate`
Generate or modify code using local models with agentic validation.

**Input Schema:**
- `instruction` (string, required) — What to generate or modify.
- `file_path` (string, optional) — Target file path for code changes.
- `work_dir` (string, optional) — Working directory for validation (go build/test).
- `context` (string, optional) — Existing code or file contents for context.
- `conversation_id` (string, optional) — For multi-turn conversations.

**Output:** Generated code, file changes (path + content + action), validation errors if any, routing metadata (which model handled it, whether it escalated to cloud).

**Maps to:** `StreamProcessRequest` RPC with intent=Coding.

### 4.2 `cercano_chat`
Ask questions, get explanations, or discuss code using local models.

**Input Schema:**
- `message` (string, required) — The question or discussion prompt.
- `context` (string, optional) — Code or file contents for reference.
- `conversation_id` (string, optional) — For multi-turn conversations.

**Output:** Natural language response, routing metadata.

**Maps to:** `ProcessRequest` RPC with intent=Chat (no workDir/fileName).

### 4.3 `cercano_review`
Review code for issues, improvements, or adherence to standards.

**Input Schema:**
- `code` (string, required) — The code to review.
- `instructions` (string, optional) — Specific review criteria or focus areas.
- `file_path` (string, optional) — File path for context.

**Output:** Review feedback as natural language.

**Maps to:** `ProcessRequest` RPC with a review-oriented prompt template.

### 4.4 `cercano_summarize`
Summarize code, files, or documentation using local models.

**Input Schema:**
- `content` (string, required) — The content to summarize.
- `format` (string, optional) — Desired output format (e.g., "bullet points", "one paragraph").

**Output:** Summary text.

**Maps to:** `ProcessRequest` RPC with a summarization prompt template.

### 4.5 `cercano_classify`
Use the SmartRouter to classify a task's complexity and recommended routing.

**Input Schema:**
- `query` (string, required) — The task description to classify.

**Output:** Intent classification (coding/chat), recommended provider (local/cloud), confidence score.

**Maps to:** SmartRouter's `ClassifyIntent` and `SelectProvider` — requires either a new lightweight RPC on the gRPC server or direct SmartRouter access.

### 4.6 `cercano_config`
Query or update Cercano's runtime configuration.

**Input Schema:**
- `action` (string, required) — "get" or "set".
- `local_model` (string, optional) — Local model name to set.
- `cloud_provider` (string, optional) — Cloud provider to set.
- `cloud_model` (string, optional) — Cloud model to set.

**Output:** Current configuration state.

**Maps to:** `UpdateConfig` RPC (for set) or a new status RPC (for get).

## 5. MCP Transport

### 5.1 stdio (Primary)
The default transport for CLI-based agents like Claude Code. The MCP server reads JSON-RPC from stdin and writes to stdout. This is the standard MCP integration path.

### 5.2 SSE (Future)
Server-Sent Events transport for browser-based or network agents. Out of scope for the initial implementation but the architecture should not preclude it.

## 6. Requirements

### 6.1 MCP SDK Integration
- Use the official Go MCP SDK (`github.com/mark3labs/mcp-go` or the reference implementation) for protocol handling.
- The SDK handles JSON-RPC framing, tool registration, and transport abstraction.

### 6.2 gRPC Client
- The MCP server connects to the Cercano gRPC server using the existing proto-generated client stubs.
- Connection target is configurable (default: `localhost:50052`).

### 6.3 Tool Registration
- All tools are registered with the MCP SDK at startup with their names, descriptions, and input schemas.
- Tool descriptions should be optimized for agent discovery (clear, keyword-rich).

### 6.4 Prompt Templates
- `cercano_review` and `cercano_summarize` wrap user input in prompt templates before sending to the gRPC server.
- Templates are defined in the MCP package, not in the core agent.

### 6.5 Error Handling
- gRPC errors are translated into MCP error responses with meaningful messages.
- Connection failures to the gRPC server produce clear diagnostics.

### 6.6 Configuration
- Environment variables: `CERCANO_GRPC_ADDR` (default: `localhost:50052`).
- The MCP server can optionally auto-launch the gRPC server if not running (stretch goal).

## 7. Acceptance Criteria
- [ ] Claude Code can discover and invoke Cercano MCP tools via stdio transport.
- [ ] `cercano_generate` produces valid code changes with validation feedback.
- [ ] `cercano_chat` returns natural language responses from local models.
- [ ] `cercano_review` and `cercano_summarize` produce useful output.
- [ ] `cercano_classify` returns intent and routing metadata.
- [ ] `cercano_config` can query and update runtime configuration.
- [ ] Multi-turn conversations work across MCP tool calls via conversation_id.
- [ ] The gRPC server is completely unchanged — MCP is a pure client/adapter.
- [ ] The MCP server can be configured and started independently of IDE extensions.

## 8. Out of Scope
- SSE transport (future).
- MCP Resources or Prompts (only Tools are implemented).
- Auto-launching the gRPC server from the MCP process (stretch goal, not required).
- Changes to the gRPC proto or server implementation.
- Agent Skills integration (separate feature).
- VS Code or Zed extension changes.
