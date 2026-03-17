# Cercano

Cercano is a local-first, AI development experience that provides a hybrid local/cloud AI development experience. Enabling development tasks to use a local LLM first approach and then fall back to cloud models when the task is either unsuited for local models, or the local model begins to spin its wheels. Potentailly providing a faster, more efficient, and cost-effective workflow for developers.

By combining the speed of local models with the power of cloud-based AI, Cercano creates a "Mixture of Experts" (MoE) architecture that intelligently routes tasks to the most appropriate model.

## Key Features

- **Smart Router** - An intelligent classifier that determines whether a request can be handled locally (faster, no cost) or requires a cloud model (higher capability). The router uses embeddings to classify the requests so it's ultra-fast and doesn't rely on the unpredictability of a model.
- **Local-First Architecture** - Utilizes [Ollama](https://ollama.com/) to run powerful open-source models (like qwen3-coder, GLM4.7-Flash, etc.) locally on your machine.
- **Cloud Fallback** - Seamlessly integrates with Google Gemini and Anthropic Claude for complex tasks that exceed local model capabilities.
- **Agentic Self-Correction** - An iterative loop that automatically validates generated code (e.g., via compilation) and requests fixes if errors are detected.
- **MCP Server** - Expose Cercano as an [MCP](https://modelcontextprotocol.io/) server, allowing cloud-based agents like Claude Code and Cursor to delegate work to local models — faster, private, and at zero cost. Supports chat queries, agentic code generation, runtime model switching, and multi-turn conversations.
- **IDE Integration** - Decoupled gRPC-based architecture allows for integration into modern IDEs like VS Code and Zed.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                 CLIENT INTEGRATIONS                     │
│   ┌───────────┐   ┌───────────┐   ┌───────────────┐     │
│   │  VS Code  │   │    Zed    │   │    Others     │     │
│   └─────┬─────┘   └─────┬─────┘   └──────┬────────┘     │
└─────────┼───────────────┼────────────────┼──────────────┘
          └───────────────┼────────────────┘
                          │ gRPC
┌─────────────────────────┴───────────────────────────────┐
|                  CERCANO SERVER                         |
|                         |                               |
|                   ┌─────┴──────┐                        |
|                   │    gRPC    │                        |
|                   │   Server   │                        |
|                   └─────┬──────┘                        |
|                         |                               |
│                       AGENT                             │
│                         |                               │
│  ┌─────────────┐  ┌──────────────┐  ┌────────────────┐  │
│  │   Smart     │  │  Coordinator │  │  Conversation  │  │
│  │   Router    │  │  (LoopAgent) │  │     Store      │  │
│  └─────────────┘  └──────────────┘  └────────────────┘  │
│                                                         │
└────────────┬────────────────────────────┬───────────────┘
             │                            │
┌────────────┴────────────┐  ┌────────────┴────────────────┐
│    Local Model          │  │       Cloud Models          │
│    (Ollama)             │  │      (Gemini, Claude)       │
└─────────────────────────┘  └─────────────────────────────┘
```

- **Core Agent (Go)** - The heart of the system, written in Go. It handles model routing, agentic loops, conversation history, and provides a gRPC interface.
- **Smart Router** - Uses semantic classification (via embeddings) to disambiguate user requests and optimize prompt delivery.
- **Coordinator (LoopAgent)** - Google ADK-backed iterative loop that generates code, validates it, and self-corrects with escalation to cloud models.
- **Conversation Store** - Server-side multi-turn history so the LLM can resolve references across requests.
- **Clients** - VS Code (TypeScript), Zed (Rust - still under construction), with gRPC for inter-process communication. The gRPC server enables any kind of client integration. Today, VS Code and Zed are provided as examples.

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
- [Ollama](https://ollama.com/) with the following models pulled:
  ```bash
  ollama pull qwen3-coder
  ollama pull nomic-embed-text
  ```
- [VS Code](https://code.visualstudio.com/) (1.90+)
- [Node.js](https://nodejs.org/) (20+) and npm

### Quick Start

1. Clone the repository:
   ```bash
   git clone https://github.com/bryan-costanich/Cercano.git
   cd Cercano
   ```

2. Install the VS Code extension dependencies:
   ```bash
   cd source/clients/vscode
   npm install
   ```

3. Open the VS Code extension workspace:
   ```bash
   code source/clients/vscode
   ```

4. Press **F5** to launch. This will:
   - Build the Go server binary
   - Compile the TypeScript extension
   - Open a new VS Code window (Extension Development Host)
   - Automatically start the Cercano server (with Ollama pre-flight check)

5. In the Extension Development Host, open the Chat panel and type `@cercano` followed by your question.

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

1. Build the MCP server:
   ```bash
   cd source/server
   make mcp
   ```

2. Add to Claude Code (choose one):

   **Via CLI:**
   ```bash
   claude mcp add --transport stdio cercano -- /path/to/Cercano/source/server/bin/cercano-mcp --grpc-addr localhost:50052
   ```

   **Via `.mcp.json` (project scope):**
   ```json
   {
     "mcpServers": {
       "cercano": {
         "type": "stdio",
         "command": "/path/to/Cercano/source/server/bin/cercano-mcp",
         "args": ["--grpc-addr", "localhost:50052"]
       }
     }
   }
   ```

3. Ensure the Cercano gRPC server is running:
   ```bash
   cd source/server
   make agent && bin/agent
   ```

### MCP Tools

| Tool | Description |
|------|-------------|
| `cercano_local` | Run any prompt against local models. When `file_path` and `work_dir` are provided, uses the agentic generate-validate loop. Otherwise, processes as a direct LLM call. |
| `cercano_config` | Update runtime configuration (local model, cloud provider/model) without restarting the server. |

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

## Development

Cercano is in active development. For detailed information on the project's goals and technical decisions, refer to the documents in the `conductor/` directory.

### Building

```bash
cd source/server
make all    # Build both agent and MCP server
make test   # Run all tests
```

## Feature TODOs

### Existing Improvements

* **Better VS Code Agent Window Integration** - The Cercano model should be available as a drop down in the agent window, as a sibling to things like "Gemini 3.1", "claude", etc.
* **LLM-Based Conversation Compaction** - Conversation history currently uses simple compaction: chat responses are truncated at 2000 characters and coding responses are reduced to `[Code generated: ACTION path]`. This works well for short exchanges but may lose important nuance in longer conversations. Revisit with LLM-based summarization for more sophisticated compaction.
* **Per-Model Configuration** - Add configurable per-model settings such as context window size, classification similarity thresholds, conversation history depth, compaction limits, and other model-specific parameters. Currently these are hardcoded constants shared across all models.
* **Simplify Provider Routing** - The SmartRouter's provider routing step (local vs cloud) uses embedding-based classification, but embeddings capture semantic meaning, not task complexity. This leads to mediocre similarity scores for straightforward queries. Since the system is local-first by design and the coordinator already handles escalation to cloud during coding tasks (after repeated validation failures), the provider routing step may be redundant. Explore removing it in favor of always routing to local and relying on the coordinator's built-in escalation logic for cloud usage.
* **Zed Extension** - The Zed client (`source/clients/zed/`) is a stub. Build out the Rust-based Zed extension to connect to the Cercano gRPC server with feature parity to the VS Code extension.

### New Features

* **User-Friendly Distribution** - Setup/launch scripts for quick onboarding, Docker containerization for one-command deployment, and a CI/CD pipeline with GitHub Actions for automated releases (cross-platform binaries and Docker images on tagged commits).
* **Add Gemma Support** - Add Google's Gemma models to the supported local model list for Ollama.
* **Agent Skills & Tool Use** - Adopt the [Agent Skills](https://agentskills.io) open standard to codify Cercano's tool use capabilities. Agent Skills is a portable, file-based format (SKILL.md) for giving agents discoverable capabilities, supported by 25+ agent products including Claude Code, Cursor, Copilot, and Codex. Cercano should support skills as both a consumer (discover and activate community/enterprise skills) and a provider (package Cercano's local inference capabilities as skills other agents can use). Includes a comprehensive audit of agent features across the landscape — both open source (Codex, Aider, Continue, Cody, OpenHands, SWE-Agent) and closed source (Claude Code, Cursor, Windsurf, GitHub Copilot, JetBrains AI) — to produce a feature matrix reference document informing capability decisions. This track should also revisit the MCP tool surface design — evaluate whether a single flexible tool (`cercano_local`) is sufficient or whether specialized tools (review, summarize, refactor, etc.) provide better agent ergonomics informed by the Agent Skills model and real-world usage patterns.
* **Remote/External Inference** - Support running inference on remote machines (e.g., Ollama on a LAN Mac Studio) and external AI accelerators (e.g., tiiny.ai). The Ollama URL is already configurable, but this feature would make remote inference robust and first-class, with service discovery and hardware-aware routing.
* **AI Engine Agnosticism** - Abstract the local inference layer so Cercano is not coupled to Ollama. Support pluggable inference backends including ONNX Runtime, Enso, and other popular AI engines, allowing users to choose the runtime best suited to their hardware and models.