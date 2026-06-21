# Docker Deployment

> Status: Planned — not yet built. Migrated from `conductor/tracks/docker_20260320/`.

## Overview / Goal

Provide a Docker image for headless and LAN server deployments of Cercano. Cercano currently runs as a native binary on the host. For headless servers and LAN deployments (e.g., a Mac Studio serving multiple clients), a Docker image simplifies setup and management. The container runs the Cercano gRPC server and connects to Ollama on the host or network.

### Key Changes
- **Dockerfile** — Multi-stage build (Go build + alpine runtime), small image (<50MB).
- **Docker Compose** — One-command startup with Ollama networking pre-configured.
- **GHCR publishing** — Release workflow pushes Docker images alongside binaries.

### What does NOT change
- The gRPC interface — clients connect to the container the same way they connect to a native binary.
- Ollama stays on the host — GPU/Metal passthrough constraints mean Ollama can't run inside the container.

## Design / Approach

### Architecture

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

### Requirements

**Dockerfile**
- Multi-stage: `golang` build stage + `alpine` runtime.
- Final image under 50MB.
- `OLLAMA_URL` defaults to `host.docker.internal:11434`.
- Exposes port 50052.

**Docker Compose**
- Service for Cercano container.
- Pre-configured networking to reach host Ollama.
- Volume mount for `~/.config/cercano/` (persistent config).
- `docker compose up` should just work.

**CI/CD Integration**
- Release workflow publishes Docker image to GHCR on tagged `v*` commits.
- Image tagged with version and `latest`.

### Acceptance Criteria
- [ ] `docker build` produces a working image under 50MB.
- [ ] `docker compose up` starts Cercano and connects to host Ollama.
- [ ] Clients can connect to the containerized gRPC server on port 50052.
- [ ] Release workflow pushes image to GHCR on tags.

### Out of Scope
- Ollama inside the container (GPU passthrough constraints).
- Kubernetes deployment.
- Multi-architecture Docker images (single linux/amd64 for now).

## Plan / Tasks

### Phase 1: Dockerfile & Local Testing

Objective: Multi-stage Dockerfile that builds Cercano and runs it in a minimal alpine container.

- [ ] Task: Create multi-stage `Dockerfile`.
    - [ ] Build stage: `golang` base, `go build` with ldflags.
    - [ ] Runtime stage: `alpine`, copy binary only.
    - [ ] Default `OLLAMA_URL=http://host.docker.internal:11434`.
    - [ ] Expose port 50052.
    - [ ] Target image size under 50MB.
- [ ] Task: Create `docker-compose.yml`.
    - [ ] Cercano service with port mapping.
    - [ ] Host networking or `host.docker.internal` for Ollama access.
    - [ ] Volume mount for persistent config.
- [ ] Task: Verify `docker compose up` connects to host Ollama.
- [ ] Task: Verify gRPC clients can connect to containerized server.
- [ ] Task: Conductor - User Manual Verification 'Dockerfile & Local Testing' (Protocol in workflow.md)

### Phase 2: CI/CD Integration

Objective: Publish Docker images to GHCR on tagged releases.

- [ ] Task: Add Docker build+push step to `.github/workflows/release.yml`.
    - [ ] Build and push to `ghcr.io/bryancostanich/cercano`.
    - [ ] Tag with version and `latest`.
- [ ] Task: Test with a release tag.
- [ ] Task: Conductor - User Manual Verification 'CI/CD Integration' (Protocol in workflow.md)
