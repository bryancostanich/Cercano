# Cercano as MCP Server

## Overview
Cercano exposes its local inference capabilities as a Model Context Protocol (MCP) server, letting cloud-based agents (Claude Code, Cursor, Copilot, etc.) delegate suitable work — code generation, chat, classification, config — to local models. The value: cloud agents offload work to local inference that is faster, private, and zero-cost, instead of burning cloud tokens. The MCP server is a thin adapter in front of Cercano's existing gRPC server; it discovers and serves tools that any MCP-compatible agent can invoke.

## Design / Architecture
The MCP server runs as a separate process (`cmd/mcp/`, `internal/mcp/`) that connects to the Cercano gRPC server as a client. Architecture flow:

```
Cloud Agent ──MCP (stdio)──► Cercano MCP Server (thin adapter)
                                     │ gRPC
                                     ▼
                             Cercano gRPC Server (unchanged)
                             Agent → Router → Provider → Coordinator → Validator
```

The separate-process design was chosen over embedding MCP into the gRPC server for: separation of concerns (transport logic stays out of the core), independent lifecycle (start/stop without affecting IDE clients), multi-transport flexibility (stdio now, SSE later), and simpler testing (adapter testable against a mock gRPC client). The gRPC server, agent orchestrator, SmartRouter, coordinator, providers, and IDE extensions are entirely unchanged — MCP is a pure consumer of the existing gRPC API.

Built on the official Go MCP SDK (`modelcontextprotocol/go-sdk` v1.4.1, maintained by the MCP org and Google), which handles JSON-RPC framing, tool registration, and transport. The gRPC connection target is configurable via `--grpc-addr` flag / `CERCANO_GRPC_ADDR` env var (default `localhost:50052`). Build targets added via Makefile (`make mcp`, `make agent`, `make all`, `make test`, `make clean`).

## Key behaviors / capabilities
- **stdio transport** — default path for CLI agents like Claude Code; reads JSON-RPC from stdin, writes to stdout.
- **`cercano_local` tool** — single flexible tool that runs any prompt against local models, mapped to the `ProcessRequest` gRPC call. The SmartRouter classifies intent internally: if `work_dir` and `file_path` are provided it routes to the coding path (agentic generate-validate loop); otherwise a direct LLM call. Returns output text, file changes, validation errors, and routing metadata (model used, escalation status). Input schema: prompt, file_path, work_dir, context, conversation_id.
- **`cercano_config` tool** — runtime configuration management. The `set` action maps to `UpdateConfig` gRPC (local_model, cloud_provider, cloud_model). Model switches confirmed in subsequent response metadata.
- **Multi-turn conversations** — `conversation_id` is passed through gRPC across sequential MCP tool calls, preserving context.
- **Error handling** — actionable diagnostics for common failures: gRPC server not running ("connection refused"), Ollama not running ("ollama serve" suggestion), server unavailable.
- Verified end-to-end with Claude Code: tool discovery, chat queries (qwen3-coder), and config-driven model switching (GLM-4.7-Flash).

## Notable decisions / constraints
- Implemented as a pure client/adapter — zero changes to the gRPC proto or server implementation.
- The spec originally enumerated specialized tools (`cercano_generate`, `cercano_chat`, `cercano_review`, `cercano_summarize`, `cercano_classify`); these were collapsed into the single flexible `cercano_local` tool, with specialized tools deferred to the Agent Skills & Tool Use track to evaluate whether they improve agent ergonomics.
- `cercano_config` `get` action deferred — no existing gRPC RPC for querying config at the time.
- SSE transport, MCP Resources/Prompts, and auto-launching the gRPC server were left out of scope (future / stretch).
