# Cercano

Cercano is a local-first, AI development experience that provides a hybrid local/cloud AI development experience. Enabling development tasks to use a local LLM first approach and then fall back to cloud models when the task is either unsuited for local models, or the local model begins to spin its wheels. Potentailly providing a faster, more efficient, and cost-effective workflow for developers.

By combining the speed of local models with the power of cloud-based AI, Cercano creates a "Mixture of Experts" (MoE) architecture that intelligently routes tasks to the most appropriate model.

## Key Features

- **Smart Router** - An intelligent classifier that determines whether a request can be handled locally (faster, no cost) or requires a cloud model (higher capability). The router uses embeddings to classify the requests so it's ultra-fast and doesn't rely on the unpredictability of a model.
- **Local-First Architecture** - Utilizes [Ollama](https://ollama.com/) to run powerful open-source models (like qwen3-coder, GLM4.7-Flash, etc.) locally on your machine.
- **Cloud Fallback** - Seamlessly integrates with Google Gemini and Anthropic Claude for complex tasks that exceed local model capabilities.
- **Agentic Self-Correction** - An iterative loop that automatically validates generated code (e.g., via compilation) and requests fixes if errors are detected.
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

Detailed setup instructions for the core agent, including prerequisites like Go and Ollama, can be found in the [Server README](source/server/README.md).

### Quick Start (Server)

1. Ensure you have **Go** and **Ollama** installed.
2. Clone the repository and navigate to the `source/server` directory.
3. Build the agent:
   ```bash
   cd source/server
   go build -o bin/agent ./cmd/agent
   ```
4. Run the agent:
   ```bash
   go run cmd/agent/main.go
   ```

## Development

Cercano is in active development. For detailed information on the project's goals and technical decisions, refer to the documents in the `conductor/` directory.


## Feature TODOs

* **Better VS Code Agent Window Integration** - The Cercano model should be available as a drop down in the agent window, as a sibling to things like "Gemini 3.1", "claude", etc.
* **Automatic Server Launch** - The Cercano server should be automatically launched when the VS Code extension is activated, and should be automatically shut down when the VS Code extension is deactivated.
* **LLM-Based Conversation Compaction** - Conversation history currently uses simple compaction: chat responses are truncated at 2000 characters and coding responses are reduced to `[Code generated: ACTION path]`. This works well for short exchanges but may lose important nuance in longer conversations. Revisit with LLM-based summarization for more sophisticated compaction.
* **Per-Model Configuration** - Add configurable per-model settings such as context window size, classification similarity thresholds, conversation history depth, compaction limits, and other model-specific parameters. Currently these are hardcoded constants shared across all models.
* **Server Debug Output in Extension** - Surface server-side logs (intent classification, provider routing decisions, coordinator events) in the VS Code extension output channel for easier debugging during development.