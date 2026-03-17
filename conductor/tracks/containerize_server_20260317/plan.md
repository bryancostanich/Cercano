# Track Plan: Containerize Go Server

## Phase 1: Dockerfile & Image Build

### Objective
Create a multi-stage Dockerfile that produces a minimal, production-ready container image for the Cercano Go server.

### Tasks
- [ ] Task: Create `Dockerfile` in the project root.
    - [ ] Build stage: `golang` base image, copy source, compile `cmd/agent/main.go` with `CGO_ENABLED=0` for static linking.
    - [ ] Runtime stage: `alpine` base, copy binary, set entrypoint.
    - [ ] Expose port 50052.
    - [ ] Set default environment variables (`OLLAMA_URL`, `CERCANO_PORT`, `CERCANO_LOCAL_MODEL`).
- [ ] Task: Add `.dockerignore` to exclude unnecessary files (`.git`, `node_modules`, IDE configs, `conductor/`, `test/`).
- [ ] Task: Build the image and verify it starts successfully.
    - [ ] Verify image size is under 50 MB.
    - [ ] Verify the binary runs and listens on port 50052.
- [ ] Task: Conductor - User Manual Verification 'Dockerfile & Image Build' (Protocol in workflow.md)

## Phase 2: Docker Compose & Networking

### Objective
Create a Docker Compose configuration for one-command startup with proper host networking for Ollama access.

### Tasks
- [ ] Task: Create `docker-compose.yml` in the project root.
    - [ ] Define `cercano-server` service using the built image.
    - [ ] Map port `50052:50052`.
    - [ ] Configure environment variables with defaults.
    - [ ] Add `extra_hosts` or network mode for `host.docker.internal` access (macOS Docker Desktop).
- [ ] Task: Create `.env.example` with documented environment variables.
    - [ ] `OLLAMA_URL`, `CERCANO_LOCAL_MODEL`, `CERCANO_PORT`, `GEMINI_API_KEY`.
- [ ] Task: Test `docker compose up` and verify:
    - [ ] Server starts and connects to host Ollama.
    - [ ] gRPC requests work from the host (via `grpcurl` or the VS Code extension).
    - [ ] Streaming responses work through the port mapping.
- [ ] Task: Test on both macOS (Docker Desktop) and Linux (if available) to verify Ollama connectivity.
- [ ] Task: Conductor - User Manual Verification 'Docker Compose & Networking' (Protocol in workflow.md)

## Phase 3: Health Check & Graceful Shutdown

### Objective
Ensure the container handles lifecycle events properly — health monitoring and clean shutdown.

### Tasks
- [ ] Task: Add a health check to the Dockerfile or Compose file.
    - [ ] Option A: `grpc_health_probe` binary in the container.
    - [ ] Option B: Simple TCP check on port 50052.
    - [ ] Choose the simplest approach that provides meaningful health status.
- [ ] Task: Verify the Go server handles `SIGTERM` gracefully.
    - [ ] gRPC server should drain active connections before exiting.
    - [ ] If `cmd/agent/main.go` doesn't handle signals, add signal handling.
- [ ] Task: Test `docker compose down` stops the container cleanly (no orphaned processes, no error logs).
- [ ] Task: Conductor - User Manual Verification 'Health Check & Graceful Shutdown' (Protocol in workflow.md)

## Phase 4: Build Scripts & Documentation

### Objective
Add convenience scripts and user-facing documentation for running Cercano via Docker.

### Tasks
- [ ] Task: Create `scripts/docker-build.sh`.
    - [ ] Build with tagging: `cercano-server:latest` and `cercano-server:<git-short-sha>`.
    - [ ] Print image size after build.
- [ ] Task: Update README.md with Docker usage section.
    - [ ] Prerequisites (Docker Desktop, Ollama running on host).
    - [ ] Quick start with `docker compose up`.
    - [ ] Environment variable reference.
    - [ ] Note about `cercano.server.autoLaunch = false` in VS Code settings.
    - [ ] Troubleshooting: Ollama connectivity from container.
- [ ] Task: Test the full end-user workflow from scratch:
    - [ ] Clone repo → `docker compose up` → open VS Code → `@cercano` query → get response.
- [ ] Task: Conductor - User Manual Verification 'Build Scripts & Documentation' (Protocol in workflow.md)
