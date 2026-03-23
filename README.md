# Cercano

Cercano is a local-first AI development tool that runs open-source models on your own hardware — fast, private, and at zero cost. Currently powered by [Ollama](https://ollama.com/), with pluggable backend support planned.

Cercano works in two ways:

**1. Local, in-agent Tool ** — Plug Cercano into cloud-based agents like Claude Code, Cursor, or Copilot via [MCP](https://modelcontextprotocol.io/). Instead of sending everything to the cloud, Cercano supercharges your frontier coding experience by providing a set of skills running locally, such as _summarization_, _extraction_, _classification_, and _code explanation_ that can not only massively reduce your cloud context window and usage (and costs), but actually provide better context to the cloud model.

**2. Standalone Agent** — Use Cercano directly as your AI coding assistant. It routes tasks to local models first, falls back to cloud when needed, and runs an agentic loop that generates, validates, and self-corrects code automatically. Integrates with VS Code and other IDEs via gRPC. Please note that Cercano's stand-alone agent is still relatively primitive and is undergoing rapid development.

## Key Features

### Core
- **Local-First Architecture** — Run powerful open-source models (qwen3-coder, GLM-4.7-Flash, etc.) locally via [Ollama](https://ollama.com/).
- **Cloud Fallback** — Seamless integration with Google Gemini and Anthropic Claude for tasks that exceed local model capabilities.
- **Smart Router** — Embedding-based classifier routes requests to local or cloud models. Ultra-fast, no LLM call needed for routing.
- **Agentic Self-Correction** — Iterative loop that generates code, validates it (e.g., via compilation), and self-corrects automatically.
- **Remote Inference** — Point Cercano at a remote Ollama instance (e.g., a Mac Studio on your LAN) for access to larger models. Runtime-configurable with automatic fallback if the remote goes down.

### Local Co-Processor Tools (via MCP)
When used as a co-processor inside cloud agents, Cercano provides specialized tools that keep work local:

| Tool | What it does | Why local? |
|------|-------------|------------|
| `cercano_summarize` | Condense files, logs, or text into concise summaries | Keep large content out of cloud context windows |
| `cercano_extract` | Pull specific info (errors, signatures, config) from large text | Filter noise locally, send only what matters |
| `cercano_classify` | Triage errors, logs, or code with category + confidence | Quick local triage without cloud round-trip |
| `cercano_explain` | Explain what code does, its components and data flow | Understand code locally before deciding what to send to cloud |
| `cercano_local` | General-purpose prompt execution against local models | Offload any simple task to local inference |

### Integration
- **MCP Server** — Expose all tools to any [MCP](https://modelcontextprotocol.io/)-compatible agent (Claude Code, Cursor, Copilot, etc.).
- **IDE Integration** — VS Code extension with gRPC-based architecture. Zed extension in progress.
- **Model Discovery** — Query available models on any Ollama instance via `cercano_models`.
- **Runtime Configuration** — Switch models, Ollama endpoints, and cloud providers on the fly via `cercano_config`.

## Architecture

Cercano can run as a standalone gRPC server (for IDE clients) or embedded inside an MCP host (for cloud agents like Claude Code). Both modes share the same core engine.

```
  Standalone Mode                    Co-Processor Mode
  (IDE clients)                      (Cloud agents)

  ┌───────────┐                      ┌──────────────┐
  │  VS Code  │                      │  Claude Code │
  │  Zed, etc │                      │  Cursor, etc │
  └─────┬─────┘                      └──────┬───────┘
        │ gRPC                              │ MCP (stdio)
        │                                   │
┌───────┴───────────────────┐  ┌────────────┴────────────────┐
│    CERCANO SERVER         │  │    CERCANO (embedded)       │
│                           │  │                             │
│  ┌──────────────────────┐ │  │  ┌───────────────────────┐  │
│  │       Agent          │ │  │  │  MCP Tool Handlers    │  │
│  │  ┌───────┐ ┌───────┐ │ │  │  │  summarize, extract,  │  │
│  │  │Router │ │ Loop  │ │ │  │  │  classify, explain    │  │
│  │  └───────┘ └───────┘ │ │  │  └───────────┬───────────┘  │
│  └──────────┬───────────┘ │  │              │              │
│             │             │  │        ┌─────┴──────┐       │
│             │             │  │        │   Agent    │       │
└─────────────┼─────────────┘  │        └─────┬──────┘       │
              │                └──────────────┼──────────────┘
              │                               │
     ┌────────┴────────┐             ┌────────┴────────┐
     │  Ollama         │             │  Ollama         │
     │  (local/remote) │             │  (local/remote) │
     └─────────────────┘             └─────────────────┘
```

- **Core Agent (Go)** — Handles model routing, agentic loops, conversation history, and provides a gRPC interface.
- **Smart Router** — Uses semantic classification (via embeddings) to route requests. Ultra-fast, no LLM call needed.
- **Coordinator (LoopAgent)** — Google ADK-backed iterative loop that generates code, validates it, and self-corrects with cloud escalation.
- **MCP Tool Handlers** — Specialized prompt templates for summarize, extract, classify, and explain. Each tool wraps the core agent with task-specific prompting.
- **Conversation Store** — Server-side multi-turn history so the LLM can resolve references across requests.

## Project Structure

- `source/server/`: The core Go-based AI agent and gRPC server.
- `source/clients/`: IDE-specific extensions.
    - `vscode/`: VS Code extension (TypeScript).
    - `zed/`: Zed extension (Rust).
- `source/proto/`: Protocol Buffer definitions for gRPC.
- `test/`: Integration and sandbox tests.
- `conductor/`: Product definitions, tech stack, and project planning documents.

## Tech Stack

- **Backend** - Go (Golang)
- **Local LLM Runtime** - Ollama (qwen3-coder, nomic-embed-text)
- **Cloud LLMs** - Google Gemini, Anthropic Claude
- **Communication** - gRPC
- **Frontend/Clients** - TypeScript (VS Code), Rust (Zed)

## Getting Started

### Prerequisites

- [Go](https://go.dev/dl/) (1.21+)
- [Ollama](https://ollama.com/) running locally

### Quick Start

```bash
git clone https://github.com/bryan-costanich/Cercano.git
cd Cercano/source/server
make build
bin/cercano setup    # checks Ollama, pulls required models, creates config
bin/cercano          # starts the gRPC server
```

### Use with Claude Code

```bash
claude mcp add --transport stdio cercano -- /path/to/bin/cercano --mcp
```

Or add to your project's `.mcp.json`. In `--mcp` mode, Cercano starts an embedded gRPC server — no separate server needed.

### Use with VS Code

1. Install the VS Code extension dependencies:
   ```bash
   cd source/clients/vscode && npm install
   ```
2. Open `source/clients/vscode` in VS Code and press **F5** to launch.
3. In the Extension Development Host, open the Chat panel and type `@cercano` followed by your question.

### Developer Workflow

```bash
cd source/server
make dev    # build + restart in one command
```

System config at `~/.config/cercano/config.yaml` persists across restarts (Ollama URL, model, port, etc.).

### Cloud Provider Setup (Optional)

Cercano is local-first — cloud providers are only used for escalation when local models can't handle a task.

1. In the Chat panel, type `@cercano /config` to open the configuration menu.
2. Set your API key (Google Gemini or Anthropic Claude).
3. Select your preferred cloud provider for escalation.

### Configuration

The following settings are available under `cercano.*` in VS Code Settings:

| Setting | Default | Description |
|---------|---------|-------------|
| `cercano.localModel` | `qwen3-coder` | Ollama model for local inference (changeable at runtime via `@cercano /config`) |
| `cercano.server.autoLaunch` | `true` | Automatically start the server on activation |
| `cercano.server.binaryPath` | *(empty)* | Override path to the server binary |
| `cercano.server.port` | `50052` | gRPC server port |
| `cercano.ollama.url` | `http://localhost:11434` | Ollama server URL |
| `cercano.provider` | `local` | Cloud provider for escalation (`google` or `anthropic`) |
| `cercano.model` | *(empty)* | Override cloud model name |

## MCP Server

Cercano can be used as an MCP (Model Context Protocol) server, allowing cloud-based agents like Claude Code and Cursor to delegate work to local models — faster, private, and at zero cost.

### Setup

1. Build Cercano:
   ```bash
   cd source/server
   make build
   ```

2. Add to Claude Code (choose one):

   **Via CLI:**
   ```bash
   claude mcp add --transport stdio cercano -- /path/to/Cercano/source/server/bin/cercano --mcp
   ```

   **Via `.mcp.json` (project scope):**
   ```json
   {
     "mcpServers": {
       "cercano": {
         "type": "stdio",
         "command": "/path/to/Cercano/source/server/bin/cercano",
         "args": ["--mcp"]
       }
     }
   }
   ```

   In `--mcp` mode, Cercano starts an embedded gRPC server automatically — no separate server process needed.

### MCP Tools

See the [tool table in Key Features](#local-co-processor-tools-via-mcp) above for the full list. Additional utility tools:

| Tool | Description |
|------|-------------|
| `cercano_models` | List models available on the active Ollama instance. Useful for discovering models on a remote machine. |
| `cercano_config` | Switch models, Ollama endpoints, or cloud providers at runtime without restarting. |

### Usage Examples

Once the MCP server is connected, your agent can call Cercano tools directly:

**Chat query (offload to local model):**
```
cercano_local(prompt: "What is a goroutine in Go? Answer in one sentence.")
→ "A goroutine is a lightweight thread of execution managed by the Go runtime."
  [Model: qwen3-coder, Confidence: 1.00, Escalated: false]
```

**Switch local model at runtime:**
```
cercano_config(action: "set", local_model: "GLM-4.7-Flash")
→ Configuration update success: updated: [local_model=GLM-4.7-Flash]
```

**Agentic code generation (with validation loop):**
```
cercano_local(
  prompt: "Add a health check endpoint that returns JSON",
  file_path: "internal/server/health.go",
  work_dir: "/path/to/project/source/server"
)
→ Generated code with automatic build validation and self-correction.
```

**Point at a remote Ollama instance:**
```
cercano_config(action: "set", ollama_url: "http://mac-studio.local:11434")
→ Configuration update success: updated: [ollama_url=http://mac-studio.local:11434]
```

**Discover available models:**
```
cercano_models()
→ Available models (2):
  - qwen3-coder:latest (4.7 GB)
  - llama3:70b (39.1 GB)
```

**Summarize a file locally (keep large content out of cloud context):**
```
cercano_summarize(file_path: "internal/agent/router.go", max_length: "brief")
→ "This Go package implements a smart routing system that selects between local
   and cloud AI models based on semantic similarity of user requests."
```

**Extract specific info from large text:**
```
cercano_extract(text: "<500 lines of logs>", query: "error and warning messages")
→ WARN  Remote endpoint health check failed (attempt 1/3)
  ERROR Remote endpoint unreachable after 3 attempts, falling back to local
```

**Classify/triage an error locally:**
```
cercano_classify(
  text: "panic: runtime error: invalid memory address or nil pointer dereference",
  categories: "bug, config issue, infra problem"
)
→ Category: bug
  Confidence: high
  Reasoning: Nil pointer dereference is a programming bug in the code logic.
```

**Explain unfamiliar code before deciding what to send to cloud:**
```
cercano_explain(file_path: "internal/agent/router.go")
→ This code implements a smart routing system for an AI agent that selects
  between local and cloud models based on semantic similarity...
```

**Multi-turn conversation:**
```
cercano_local(prompt: "Explain the SmartRouter", conversation_id: "abc123")
cercano_local(prompt: "How does it handle escalation?", conversation_id: "abc123")
→ Second call has full context from the first.
```

### Verified Agents

| Agent | Status |
|-------|--------|
| Claude Code | Verified — tool discovery, chat queries, config updates, model switching |
| Cursor | Not yet tested |

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--grpc-addr` | `localhost:50052` | Address of the Cercano gRPC server |

## Agent Skills

Cercano publishes its tools as [Agent Skills](https://agentskills.io) — an open standard for packaging AI capabilities so they're discoverable by any compatible agent. Over 30 agents support this standard, including Claude Code, Cursor, Copilot, Gemini CLI, Codex, and more.

### Published Skills

| Skill | Description |
|-------|-------------|
| `cercano-local` | General-purpose local inference — chat queries and agentic code generation |
| `cercano-summarize` | Summarize text or files locally (brief, medium, or detailed) |
| `cercano-extract` | Pull specific information from text (errors, signatures, config values) |
| `cercano-classify` | Categorize/triage text with confidence scores and reasoning |
| `cercano-explain` | Explain code — what it does, key interfaces, and data flow |
| `cercano-config` | View/change Cercano's runtime configuration |
| `cercano-models` | List available models on the connected Ollama instance |

Each skill is a `SKILL.md` file that tells the agent what the tool does, its parameters, and how to invoke it via MCP.

### How Agents Discover Skills

Agents scan well-known directories for `SKILL.md` files at startup:

| Directory | Discovered by |
|-----------|---------------|
| `.agents/skills/<skill-name>/SKILL.md` | Any Agent Skills-compatible agent |
| `.claude/skills/<skill-name>/SKILL.md` | Claude Code (also appears as slash commands) |

Cercano ships its skill definitions in both locations.

### Installing Skills in Your Project

To make Cercano's skills available to your agent, copy the skill files into your project:

```bash
# For any Agent Skills-compatible agent
cp -r /path/to/Cercano/.agents/skills/* .agents/skills/

# For Claude Code specifically (enables /cercano-* slash commands)
cp -r /path/to/Cercano/.claude/skills/* .claude/skills/
```

The `cercano_skills` MCP tool also provides programmatic access to skill definitions:

```
cercano_skills(action: "list")           → catalog of all skills
cercano_skills(action: "get", name: "cercano-local")  → full SKILL.md content
```

For a detailed guide on writing custom SKILL.md files, see [docs/agent-skills-guide.md](docs/agent-skills-guide.md).

## Remote Inference

Cercano can delegate inference to a remote Ollama instance — for example, another machine on your LAN with more GPU memory and larger models. The remote endpoint is runtime-configurable with automatic fallback to local Ollama if the remote goes down.

### Setup

1. Ensure Ollama is running on the remote machine and accessible over the network:
   ```bash
   # On the remote machine (e.g., mac-studio.local)
   OLLAMA_HOST=0.0.0.0 ollama serve
   ```

2. Point Cercano at the remote instance:

   **Via environment variable (at startup):**
   ```bash
   OLLAMA_URL=http://mac-studio.local:11434 bin/agent
   ```

   **Via MCP at runtime (no restart needed):**
   ```
   cercano_config(action: "set", ollama_url: "http://mac-studio.local:11434")
   ```

3. Discover available models on the remote machine:
   ```
   cercano_models()
   → Available models (3):
   - qwen3-coder:latest (4.7 GB, modified: 2026-03-15T10:30:00Z)
   - llama3:70b (39.1 GB, modified: 2026-03-14T09:00:00Z)
   - deepseek-coder-v2:latest (8.9 GB, modified: 2026-03-13T14:00:00Z)
   ```

4. Switch to a model that's only available on the remote:
   ```
   cercano_config(action: "set", local_model: "llama3:70b")
   ```

### Fallback Behavior

When a remote endpoint is configured, Cercano monitors it with periodic health checks:

- Pings the remote every 30 seconds via `GET /api/tags`
- After 3 consecutive failures, automatically switches to local Ollama
- When the remote recovers, automatically switches back
- Response metadata includes `[Endpoint: url]` or `[Endpoint: url (fallback)]` so you always know which instance served the request

No configuration is needed — fallback is automatic whenever a remote URL is set.

## Development

Cercano is in active development. For detailed information on the project's goals and technical decisions, refer to the documents in the `conductor/` directory.

### Building

```bash
cd source/server
make all    # Build both agent and MCP server
make test   # Run all tests
```

## Feature TODOs

### New Features

* **[Competitive Audit — Agent Features Landscape](conductor/tracks/competitive_audit_20260318/plan.md)** - Feature matrix across 12+ open-source and commercial agents (Codex, Aider, Continue, Cody, OpenHands, SWE-Agent, Claude Code, Cursor, Windsurf, GitHub Copilot, JetBrains AI, Amazon Q) to inform Cercano's tool design and roadmap.
* **[Semantic Codebase Search](conductor/tracks/semantic_search_20260318/plan.md)** - Embedding-based code search by intent ("find auth-related code"), not just string matching. Requires indexing pipeline, storage, and nearest-neighbor retrieval.
* **[User-Friendly Distribution](conductor/tracks/distribution_20260317/plan.md)** - Setup/launch scripts, Docker containerization, and CI/CD pipeline with GitHub Actions for automated cross-platform releases.
* **[AI Engine Agnosticism](conductor/tracks/engine_agnosticism_20260317/plan.md)** - Abstract the local inference layer to support pluggable backends (ONNX Runtime, Enso, etc.) beyond Ollama.
* **Stand-alone CLI** - Create a stand alone Command Line Interface (CLI) for cercano that doesn't really on other CLI integrations.

### Existing Improvements

* **Better VS Code Agent Window Integration** - Make Cercano available as a model dropdown in the VS Code agent window alongside Gemini, Claude, etc.
* **LLM-Based Conversation Compaction** - Replace simple truncation-based compaction with LLM-powered summarization for better context retention in long conversations.
* **Per-Model Configuration** - Configurable per-model settings (context window, classification thresholds, history depth, compaction limits) instead of hardcoded constants.
* **Simplify Provider Routing** - Evaluate removing the SmartRouter's embedding-based local/cloud routing in favor of always-local with coordinator-driven cloud escalation.
* **Zed Extension** - Build out the Rust-based Zed extension (`source/clients/zed/`) with feature parity to the VS Code extension.