# Track Specification: User-Friendly Distribution

## 1. Job Title
Make Cercano easy to install, launch, and use without requiring a Go toolchain or building from source.

## 2. Overview
Cercano currently requires users to clone the repo and build from source with a Go toolchain. This is a barrier for adoption. This track addresses three distribution concerns:

1. **Setup & Launch Scripts** — Shell scripts and documentation that let users get Cercano running with minimal steps, without needing to understand the build system.
2. **Docker Containerization** — A pre-built Docker image users can pull and run with a single command.
3. **CI/CD Pipeline & Releases** — GitHub Actions workflows that automatically build, test, and publish releases (binaries and Docker images) on tagged commits.

Ollama remains on the host — GPU/Metal passthrough is not practical in containers (especially on macOS where Metal is unavailable inside Docker). The containerized Cercano server connects to Ollama over the network.

**What changes:** Setup scripts, a Dockerfile, Docker Compose configuration, CI/CD workflows, and release automation are added.

**What does NOT change:** The Go server code, gRPC interface, IDE extensions, or any internal logic.

## 3. Architecture Decision

```
┌─────────────────────────────────┐
│         Host Machine            │
│                                 │
│  Option A: Native (via script)  │
│  ┌───────────────────────────┐  │
│  │   Cercano Go Server       │  │
│  │   (pre-built binary)      │  │
│  └─────────────┬─────────────┘  │
│                │                │
│  Option B: Docker               │
│  ┌───────────────────────────┐  │
│  │   Docker Container        │  │
│  │   Cercano Go Server       │  │
│  │   (gRPC on port 50052)    │  │
│  └─────────────┬─────────────┘  │
│                │ HTTP            │
│  ┌─────────────┴─────────────┐  │
│  │   Ollama (host)           │  │
│  │   (port 11434)            │  │
│  └───────────────────────────┘  │
│                                 │
│  IDE (VS Code / Zed) or        │
│  MCP Client (Claude Code)      │
│    connects to gRPC:50052      │
└─────────────────────────────────┘
```

Key decisions:
- **Two distribution paths** — native binary (via setup script or GitHub release download) and Docker image. Both are first-class.
- **Multi-stage Docker build** — build stage compiles the Go binary, runtime stage is minimal (alpine). Keeps image small.
- **Ollama on host, not in container** — GPU/Metal access requires host-level drivers.
- **GitHub Actions for CI/CD** — automated testing on PRs, binary builds and Docker image publishing on tagged releases.
- **Cross-platform binaries** — release builds for macOS (arm64, amd64) and Linux (amd64) at minimum.

## 4. Requirements

### 4.1 Setup & Launch Scripts
- `scripts/setup.sh` — checks prerequisites (Go, Ollama, required models), builds binaries if needed, creates config files.
- `scripts/start.sh` — starts Ollama (if not running), starts the Cercano gRPC server, and optionally the MCP server.
- `scripts/stop.sh` — gracefully stops Cercano services.
- Clear error messages with actionable instructions when prerequisites are missing.
- Getting-started documentation that covers both the "I want to build from source" and "I just want to run it" paths.

### 4.2 Dockerfile & Docker Compose
- Multi-stage build: Go build stage + minimal runtime stage.
- Build stage uses official `golang` image, compiles with CGO disabled for static linking.
- Runtime stage uses `alpine` for minimal image size (target: under 50 MB).
- Expose port 50052 (gRPC).
- Default `OLLAMA_URL` to `http://host.docker.internal:11434` for Docker Desktop compatibility.
- Docker Compose for one-command startup with port mapping and environment variables.
- `.env.example` with documented variables.
- Health check and graceful shutdown (SIGTERM handling, connection draining).

### 4.3 CI/CD Pipeline & Releases
- **CI (on PR/push to main):**
  - Run `go test ./...`
  - Build binaries to verify compilation
  - Lint (if linter is configured)
- **Release (on tagged commit, e.g., `v0.1.0`):**
  - Build cross-platform binaries (macOS arm64/amd64, Linux amd64)
  - Create GitHub Release with binaries attached
  - Build and push Docker image to GitHub Container Registry (GHCR)
  - Tag Docker image with version and `latest`
- **Makefile integration** — `make release` for local release builds

### 4.4 Configuration
- All existing environment variables must work in both native and container modes.
- Document the difference between host-mode and container-mode Ollama URLs.

### 4.5 IDE Compatibility
- The VS Code extension's `cercano.server.autoLaunch` should be set to `false` when using the containerized server or a pre-built binary launched via script.
- The extension connects to `localhost:50052` regardless of how the server is running — no extension changes needed.

## 5. Acceptance Criteria
- [ ] A new user can go from `git clone` to a working Cercano instance in under 5 minutes using the setup script.
- [ ] `docker compose up` starts the Cercano server and connects to host Ollama.
- [ ] GitHub Actions CI runs tests and builds on every PR.
- [ ] Tagged commits produce GitHub Releases with downloadable binaries.
- [ ] Tagged commits produce a Docker image published to GHCR.
- [ ] Documentation covers: native setup, Docker setup, and downloading pre-built releases.

## 6. Out of Scope
- Ollama containerization (GPU passthrough constraints, especially on macOS).
- Kubernetes deployment manifests.
- Homebrew formula or other package manager distribution (future track).
- Multi-architecture Docker images (nice to have, not required initially).
