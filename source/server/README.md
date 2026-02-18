# Cercano AI Agent

This repository contains the Go-based AI agent for the Cercano project. It includes a gRPC server for inter-process communication and a smart router that uses a local LLM (Large Language Model) to classify user requests and route them to appropriate local or cloud models.

## Getting Started

Follow these steps to set up, build, and run the AI agent.

### Prerequisites

*   **Go:** Go 1.21 or later. [Download & Install Go](https://go.dev/doc/install)
*   **Ollama:** A local LLM runtime. [Ollama Website](https://ollama.com/)
*   **Protocol Buffers Compiler (`protoc`):** Used to generate gRPC code.

### Installation

1.  **Clone the repository:**
    ```bash
    git clone https://github.com/bryan_costanich/Cercano.git
    cd Cercano
    ```

2.  **Install `protoc`:**
    On macOS:
    ```bash
    brew install protobuf
    ```
    For other operating systems, please refer to the [Protocol Buffers documentation](https://grpc.io/docs/protoc-installation/).

3.  **Install Go gRPC plugins:**
    ```bash
    go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
    ```
    Ensure your `$GOPATH/bin` is in your system's `PATH`. You can add it by running:
    ```bash
    export PATH=$PATH:$(go env GOPATH)/bin
    ```
    (You might want to add this line to your shell's profile file, e.g., `~/.zshrc` or `~/.bash_profile`, for persistence.)

4.  **Install Ollama:**
    On macOS:
    ```bash
    brew install ollama
    brew services start ollama
    ```
    For other operating systems (Linux, Windows), please refer to the [Ollama website](https://ollama.com/download).

5.  **Download the embedding and LLM models:**
    ```bash
    ollama pull nomic-embed-text
    ollama pull phi
    ```
    The smart router uses `nomic-embed-text` for semantic classification via embeddings. `phi` is used as a default local LLM for request processing.

### Build and Run the AI Agent

1.  **Navigate to the source directory:**
    ```bash
    cd source
    ```

2.  **Build the agent:**
    ```bash
    go build -o ../bin/agent ./cmd/agent
    ```

3.  **Run the agent:**
    ```bash
    ../bin/agent
    ```
    The gRPC server will start and listen on port `50052`.

### Testing the gRPC Server

While the agent is running in one terminal, you can test its gRPC endpoint using `grpcurl` in another terminal.

1.  **Install `grpcurl`:**
    On macOS:
    ```bash
    brew install grpcurl
    ```
    For other operating systems, refer to the [grpcurl GitHub page](https://github.com/fullstorydev/grpcurl).

2.  **Send a test request:**
    Navigate to the `source/proto` directory for the `.proto` file:
    ```bash
    cd source/proto
    grpcurl -plaintext -proto agent.proto -d '{"input": "What is the capital of France?"}' localhost:50052 agent.Agent/ProcessRequest
    ```
    The classification of the request will be handled by the smart router using semantic similarity against prototypes.

## Project Structure

*   `source/cmd/agent/`: Contains the main application entry point.
*   `source/internal/agent/`: Contains the gRPC server implementation, Agent orchestrator, and smart router.
*   `source/internal/llm/`: Contains model provider implementations (Ollama, Cloud/LangChainGo).
*   `source/internal/loop/`: Contains the iterative coordinator loop for agentic tasks.
*   `source/internal/tools/`: Contains code generation and validation tools.
*   `source/internal/agent/prototypes.yaml`: YAML file defining example phrases for semantic routing and intent classification.
