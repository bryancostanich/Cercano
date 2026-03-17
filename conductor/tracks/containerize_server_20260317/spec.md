# Track Specification: Containerize Go Server

## 1. Job Title
Package the Cercano Go server in a Docker container for end-user distribution and deployment.

## 2. Overview
Cercano's Go server currently requires users to have a Go toolchain installed and to build from source. This is a barrier for end-user adoption. This track containerizes the Go server so it can be distributed as a pre-built Docker image that users pull and run with a single command.

Ollama remains on the host — GPU/Metal passthrough is not practical in containers (especially on macOS where Metal is unavailable inside Docker). The containerized Cercano server connects to Ollama over the network.

**What changes:** A `Dockerfile`, Docker Compose configuration, and build/publish scripts are added. The server's Ollama URL default is adjusted to support both containerized and bare-metal scenarios.

**What does NOT change:** The Go server code, gRPC interface, IDE extensions, or any internal logic. This is purely a packaging and distribution concern.

## 3. Architecture Decision

```
┌─────────────────────────────────┐
│         Host Machine            │
│                                 │
│  ┌───────────────────────────┐  │
│  │   Docker Container        │  │
│  │                           │  │
│  │   Cercano Go Server       │  │
│  │   (gRPC on port 50052)    │  │
│  └─────────┬─────────────────┘  │
│            │ HTTP                │
│  ┌─────────┴─────────────────┐  │
│  │   Ollama (host)           │  │
│  │   (port 11434)            │  │
│  └───────────────────────────┘  │
│                                 │
│  IDE (VS Code / Zed)            │
│    connects to gRPC:50052       │
└─────────────────────────────────┘
```

Key decisions:
- **Multi-stage Docker build** — build stage compiles the Go binary, runtime stage is a minimal image (distroless or alpine). Keeps the image small.
- **Ollama on host, not in container** — GPU/Metal access requires host-level drivers. The container connects to Ollama via `host.docker.internal` (Docker Desktop) or the host network IP.
- **Docker Compose for convenience** — a `docker-compose.yml` provides a one-command startup that handles port mapping and environment variables.
- **Environment variable configuration** — all settings (Ollama URL, port, model, API keys) are passed via environment variables, consistent with existing `cmd/agent/main.go` behavior.
- **No Ollama container** — while Ollama publishes Docker images, GPU passthrough on macOS is not supported. Users run Ollama natively. A future track could add an optional Ollama service for Linux hosts with NVIDIA GPUs.

## 4. Requirements

### 4.1 Dockerfile
- Multi-stage build: Go build stage + minimal runtime stage.
- Build stage uses official `golang` image, compiles `cmd/agent/main.go` with CGO disabled for static linking.
- Runtime stage uses `alpine` or `distroless` for minimal attack surface and image size.
- Expose port 50052 (gRPC).
- Default `OLLAMA_URL` to `http://host.docker.internal:11434` for Docker Desktop compatibility.
- Health check using gRPC health probe or a simple TCP check on 50052.

### 4.2 Docker Compose
- Service definition for `cercano-server`.
- Port mapping: `50052:50052`.
- Environment variables with sensible defaults and override support.
- Optional `.env` file support for API keys and configuration.
- Network configuration to reach host Ollama.

### 4.3 Build & Publish Scripts
- `scripts/docker-build.sh` — builds the Docker image with proper tagging.
- Tagging strategy: `cercano-server:latest`, `cercano-server:<version>`, `cercano-server:<git-sha>`.
- Future: GitHub Actions workflow for automated image publishing (out of scope for this track, but the Dockerfile should be CI-friendly).

### 4.4 Configuration
- All existing environment variables must work inside the container:
  - `OLLAMA_URL` — Ollama endpoint (default: `http://host.docker.internal:11434`).
  - `CERCANO_LOCAL_MODEL` — Local model name (default: `qwen3-coder`).
  - `CERCANO_PORT` — gRPC port (default: `50052`).
  - `GEMINI_API_KEY` — Optional cloud provider key.
- Document the difference between host-mode and container-mode Ollama URLs.

### 4.5 IDE Compatibility
- The VS Code extension's `cercano.server.autoLaunch` should be set to `false` when using the containerized server.
- The extension connects to `localhost:50052` regardless of whether the server is containerized or bare-metal — no extension changes needed.
- Document this in the setup instructions.

## 5. Acceptance Criteria
- [ ] `docker build` produces a working image under 50 MB (runtime stage).
- [ ] `docker compose up` starts the Cercano server and it connects to host Ollama.
- [ ] The VS Code extension can connect to the containerized server and process requests.
- [ ] gRPC streaming works through the Docker port mapping.
- [ ] Environment variables are properly passed through for all configuration options.
- [ ] The container starts and exits cleanly (proper signal handling for graceful shutdown).
- [ ] The image does not contain the Go toolchain, source code, or build artifacts beyond the compiled binary.
- [ ] Documentation covers setup for macOS (Docker Desktop) and Linux.

## 6. Out of Scope
- Ollama containerization (GPU passthrough constraints, especially on macOS).
- CI/CD pipeline for automated image publishing (future track).
- Kubernetes deployment manifests.
- Container registry setup (Docker Hub, GHCR).
- Multi-architecture builds (ARM/x86) — nice to have but not required initially.
- MCP server containerization (depends on MCP server track completion).
