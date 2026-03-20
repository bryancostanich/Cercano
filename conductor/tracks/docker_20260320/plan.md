# Track Plan: Docker Deployment

## Phase 1: Dockerfile & Local Testing

### Objective
Multi-stage Dockerfile that builds Cercano and runs it in a minimal alpine container.

### Tasks
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

## Phase 2: CI/CD Integration

### Objective
Publish Docker images to GHCR on tagged releases.

### Tasks
- [ ] Task: Add Docker build+push step to `.github/workflows/release.yml`.
    - [ ] Build and push to `ghcr.io/bryancostanich/cercano`.
    - [ ] Tag with version and `latest`.
- [ ] Task: Test with a release tag.
- [ ] Task: Conductor - User Manual Verification 'CI/CD Integration' (Protocol in workflow.md)
