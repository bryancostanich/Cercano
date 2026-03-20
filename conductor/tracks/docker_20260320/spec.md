# Track Specification: Docker Deployment

## 1. Job Title
Docker image for headless and LAN server deployments of Cercano.

## 2. Overview
Cercano currently runs as a native binary on the host. For headless servers and LAN deployments (e.g., a Mac Studio serving multiple clients), a Docker image simplifies setup and management. The container runs the Cercano gRPC server and connects to Ollama on the host or network.

### Key Changes
- **Dockerfile** — Multi-stage build (Go build + alpine runtime), small image (<50MB).
- **Docker Compose** — One-command startup with Ollama networking pre-configured.
- **GHCR publishing** — Release workflow pushes Docker images alongside binaries.

### What does NOT change
- The gRPC interface — clients connect to the container the same way they connect to a native binary.
- Ollama stays on the host — GPU/Metal passthrough constraints mean Ollama can't run inside the container.

## 3. Architecture

```
┌───────────────────────┐
│   Docker Container    │
│  ┌─────────────────┐  │
│  │  cercano :50052 │──┼──▶ Ollama (host machine :11434)
│  └─────────────────┘  │    via host.docker.internal
│         ▲             │
└─────────┼─────────────┘
          │
   VS Code / Claude Code / Zed
```

## 4. Requirements

### 4.1 Dockerfile
- Multi-stage: `golang` build stage + `alpine` runtime.
- Final image under 50MB.
- `OLLAMA_URL` defaults to `host.docker.internal:11434`.
- Exposes port 50052.

### 4.2 Docker Compose
- Service for Cercano container.
- Pre-configured networking to reach host Ollama.
- Volume mount for `~/.config/cercano/` (persistent config).
- `docker compose up` should just work.

### 4.3 CI/CD Integration
- Release workflow publishes Docker image to GHCR on tagged `v*` commits.
- Image tagged with version and `latest`.

## 5. Acceptance Criteria
- [ ] `docker build` produces a working image under 50MB.
- [ ] `docker compose up` starts Cercano and connects to host Ollama.
- [ ] Clients can connect to the containerized gRPC server on port 50052.
- [ ] Release workflow pushes image to GHCR on tags.

## 6. Out of Scope
- Ollama inside the container (GPU passthrough constraints).
- Kubernetes deployment.
- Multi-architecture Docker images (single linux/amd64 for now).
