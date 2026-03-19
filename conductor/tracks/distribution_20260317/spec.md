# Track Specification: User-Friendly Distribution

## 1. Job Title
Make Cercano a single binary that's easy to install, configure, develop against, and distribute.

## 2. Overview
Cercano currently requires building two separate binaries from source (`bin/agent` and `bin/cercano-mcp`), manually starting the gRPC server in a separate terminal, and re-configuring settings (like remote Ollama URL) on every restart. This track addresses these friction points across three user personas:

1. **End users** — `brew install cercano`, Claude Code auto-launches it, config persists. Zero friction.
2. **Developers** — `make dev` rebuilds and restarts in one command, system config persists across restarts.
3. **LAN/server deployments** — Docker image for headless operation (stretch goal).

### Key Changes
- **Unified binary** — Merge the MCP server and gRPC server into a single `cercano` binary with mode flags.
- **System config** — Persistent config file (`~/.config/cercano/config.yaml`) loaded on startup, so settings like Ollama URL and default model survive restarts.
- **Dev workflow** — `make dev` builds + restarts in one step.
- **Setup command** — `cercano setup` checks prerequisites, pulls models.
- **Homebrew** — `brew tap` + `brew install cercano` for macOS users.
- **CI/CD** — GitHub Actions for tests on PR and cross-platform release binaries on tags.

### What does NOT change
- The gRPC interface (VS Code, Zed, and other clients connect exactly as before)
- The MCP tool surface (all tools work identically)
- The SmartRouter, agentic loop, or any AI logic
- Ollama stays on the host (GPU/Metal passthrough constraints)

## 3. Architecture Decision

### Unified Binary Modes
```
cercano                     # Start gRPC server (VS Code, Zed, etc. connect to this)
cercano --mcp               # Start MCP+gRPC embedded (Claude Code launches this)
cercano setup               # Check prereqs, pull Ollama models
cercano config              # Show/edit system config
```

### Config Hierarchy
```
~/.config/cercano/config.yaml    # System config (persisted)
         ↓ overridden by
Session runtime changes           # cercano_config(action: "set", ...)
         ↓ overridden by
Environment variables              # OLLAMA_URL, CERCANO_LOCAL_MODEL, etc.
         ↓ overridden by
CLI flags                          # --ollama-url, --model, etc.
```

### Multiple Clients
Each client (Claude Code, VS Code, Cursor) runs its own `cercano` process. The processes are lightweight — the LLM is the heavy resource, and Ollama handles concurrent requests. Config changes via `cercano_config` are session-scoped; persistent changes go to system config.

### Architecture Diagram
```
┌─────────────────────────────────────────────────┐
│                  Host Machine                    │
│                                                  │
│  Claude Code ──► cercano --mcp (embedded)        │
│                    └── gRPC server (in-process)   │
│                         │                        │
│  VS Code ──────► cercano (standalone gRPC)       │
│                         │                        │
│                    ┌────┴────┐                   │
│                    │ Ollama  │                    │
│                    │ (host)  │                    │
│                    └─────────┘                   │
└─────────────────────────────────────────────────┘
```

## 4. Requirements

### 4.1 Unified Binary
- Single `cercano` binary replaces `bin/agent` and `bin/cercano-mcp`.
- Default mode: gRPC server on port 50052.
- `--mcp` flag: embedded mode — gRPC server starts in-process + MCP on stdio.
- Subcommands: `setup`, `config`.

### 4.2 System Config
- Config file at `~/.config/cercano/config.yaml`.
- Fields: `ollama_url`, `local_model`, `cloud_provider`, `cloud_model`, `cloud_api_key`, `port`.
- Loaded on startup, session `cercano_config` overrides at runtime.
- Environment variables override config file.
- `cercano config` subcommand to view/edit.

### 4.3 Dev Workflow
- `make dev` — build unified binary + kill old process + restart.
- `make build` — just build (for when you want to restart manually).
- System config means no more reconfiguring Mac Studio URL after restarts.

### 4.4 Setup Command
- `cercano setup` — checks prerequisites (Ollama running, required models pulled).
- Auto-pulls missing models (`nomic-embed-text`, default local model).
- Prints clear actionable errors.

### 4.5 Homebrew
- Homebrew tap: `bryancostanich/cercano`.
- `brew install bryancostanich/cercano/cercano`.
- Formula builds from source or downloads release binary.

### 4.6 CI/CD
- **CI (on PR/push to main):** `go test ./...`, build verification, module cache.
- **Release (on tagged `v*`):** Cross-platform binaries (macOS arm64/amd64, Linux amd64), GitHub Release, Docker image to GHCR.

### 4.7 Docker (Stretch Goal)
- Multi-stage Dockerfile, alpine runtime, under 50MB.
- `OLLAMA_URL` defaults to `host.docker.internal:11434`.
- Docker Compose for one-command startup.
- Deferred — build if time permits, not blocking track completion.

## 5. Acceptance Criteria
- [ ] Single `cercano` binary handles both gRPC server and MCP embedded modes.
- [ ] System config persists Ollama URL, model, and other settings across restarts.
- [ ] `make dev` rebuilds and restarts in one command.
- [ ] `cercano setup` validates prerequisites and pulls models.
- [ ] `brew install` installs a working Cercano on macOS.
- [ ] CI runs tests on every PR; tagged commits produce release binaries.
- [ ] Existing VS Code and MCP clients work without changes.

## 6. Out of Scope
- Ollama containerization (GPU/Metal passthrough constraints).
- Kubernetes deployment.
- Multi-architecture Docker images.
- Windows support (future consideration).
