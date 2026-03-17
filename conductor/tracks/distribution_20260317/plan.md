# Track Plan: User-Friendly Distribution

## Phase 1: Setup & Launch Scripts

### Objective
Create shell scripts that let users set up and run Cercano without understanding the build system, and documentation that covers both "build from source" and "just run it" paths.

### Tasks
- [ ] Task: Create `scripts/setup.sh`.
    - [ ] Check for prerequisites: Go toolchain, Ollama, required Ollama models (`qwen3-coder`, `nomic-embed-text`).
    - [ ] Print clear, actionable errors when prerequisites are missing (e.g., "Ollama not found. Install from https://ollama.com/").
    - [ ] Auto-pull missing Ollama models if Ollama is available.
    - [ ] Build server binaries (`agent` and `cercano-mcp`) via `make all`.
    - [ ] Verify build succeeded and print summary.
- [ ] Task: Create `scripts/start.sh`.
    - [ ] Start Ollama if not already running.
    - [ ] Start the Cercano gRPC server in the background.
    - [ ] Optionally start the MCP server (`--with-mcp` flag).
    - [ ] Write PID files for clean shutdown.
    - [ ] Print status and connection info on success.
- [ ] Task: Create `scripts/stop.sh`.
    - [ ] Read PID files and gracefully stop Cercano services.
    - [ ] Handle cases where services are already stopped.
- [ ] Task: Update README.md Getting Started section.
    - [ ] Add "Quick Start (scripts)" path: `./scripts/setup.sh && ./scripts/start.sh`.
    - [ ] Keep existing "Build from source" path for developers.
    - [ ] Add "Download pre-built release" path (placeholder until CI/CD is done).
- [ ] Task: Conductor - User Manual Verification 'Setup & Launch Scripts' (Protocol in workflow.md)

## Phase 2: Dockerfile & Docker Image

### Objective
Create a multi-stage Dockerfile that produces a minimal, production-ready container image for the Cercano Go server.

### Tasks
- [ ] Task: Create `Dockerfile` in the project root.
    - [ ] Build stage: `golang` base image, copy source, compile `cmd/agent/main.go` with `CGO_ENABLED=0` for static linking.
    - [ ] Runtime stage: `alpine` base, copy binary, set entrypoint.
    - [ ] Expose port 50052.
    - [ ] Set default environment variables (`OLLAMA_URL`, `CERCANO_PORT`, `CERCANO_LOCAL_MODEL`).
    - [ ] Default `OLLAMA_URL` to `http://host.docker.internal:11434`.
- [ ] Task: Add `.dockerignore` to exclude unnecessary files (`.git`, `node_modules`, IDE configs, `conductor/`, `test/`).
- [ ] Task: Build the image and verify it starts successfully.
    - [ ] Verify image size is under 50 MB.
    - [ ] Verify the binary runs and listens on port 50052.
- [ ] Task: Conductor - User Manual Verification 'Dockerfile & Docker Image' (Protocol in workflow.md)

## Phase 3: Docker Compose & Networking

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
- [ ] Task: Add health check to Dockerfile or Compose file.
    - [ ] Simple TCP check on port 50052.
- [ ] Task: Verify the Go server handles `SIGTERM` gracefully (connection draining).
- [ ] Task: Test `docker compose up` and verify:
    - [ ] Server starts and connects to host Ollama.
    - [ ] gRPC requests work from the host.
    - [ ] `docker compose down` stops cleanly.
- [ ] Task: Conductor - User Manual Verification 'Docker Compose & Networking' (Protocol in workflow.md)

## Phase 4: CI/CD Pipeline

### Objective
Set up GitHub Actions for continuous integration (test on every PR) and automated releases (binaries + Docker image on tagged commits).

### Tasks
- [ ] Task: Create `.github/workflows/ci.yml`.
    - [ ] Trigger on push to `main` and on pull requests.
    - [ ] Run `go test ./...` in `source/server/`.
    - [ ] Build binaries to verify compilation succeeds.
    - [ ] Cache Go modules for faster builds.
- [ ] Task: Create `.github/workflows/release.yml`.
    - [ ] Trigger on pushed tags matching `v*` (e.g., `v0.1.0`).
    - [ ] Build cross-platform binaries:
        - [ ] macOS arm64 (Apple Silicon)
        - [ ] macOS amd64 (Intel)
        - [ ] Linux amd64
    - [ ] Create GitHub Release with binaries attached.
    - [ ] Build Docker image and push to GHCR (`ghcr.io/bryancostanich/cercano`).
    - [ ] Tag Docker image with version (`v0.1.0`) and `latest`.
- [ ] Task: Add `make release` target for local cross-platform builds.
- [ ] Task: Update README.md with:
    - [ ] CI badge.
    - [ ] Instructions for downloading pre-built binaries from GitHub Releases.
    - [ ] Docker pull command for GHCR image.
- [ ] Task: Test the full release workflow with a test tag.
- [ ] Task: Conductor - User Manual Verification 'CI/CD Pipeline' (Protocol in workflow.md)

## Phase 5: Documentation & End-to-End Verification

### Objective
Ensure all distribution paths work end-to-end and documentation is complete.

### Tasks
- [ ] Task: Test the "scripts" path end-to-end:
    - [ ] Clone repo → `./scripts/setup.sh` → `./scripts/start.sh` → query via VS Code or MCP → works.
- [ ] Task: Test the "Docker" path end-to-end:
    - [ ] Clone repo → `docker compose up` → query via VS Code or MCP → works.
- [ ] Task: Test the "release binary" path end-to-end:
    - [ ] Download binary from GitHub Release → run → query via MCP → works.
- [ ] Task: Final README review — ensure Getting Started covers all three paths clearly.
- [ ] Task: Conductor - User Manual Verification 'Documentation & End-to-End Verification' (Protocol in workflow.md)
