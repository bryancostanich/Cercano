# Track Plan: User-Friendly Distribution

## Phase 1: System Config

### Objective
Add a persistent config file so settings like Ollama URL and default model survive restarts.

### Tasks
- [ ] Task: Design config file format and location (`~/.config/cercano/config.yaml`).
    - [ ] Define fields: ollama_url, local_model, cloud_provider, cloud_model, cloud_api_key, port.
    - [ ] Define precedence: CLI flags > env vars > session overrides > config file > defaults.
- [ ] Task: Implement config loading in the server.
    - [ ] Load from `~/.config/cercano/config.yaml` on startup.
    - [ ] Fall back to defaults if file doesn't exist.
    - [ ] Environment variables override config file values.
    - [ ] Red/Green TDD.
- [ ] Task: Implement `cercano_config(action: "get")` to return effective config.
    - [ ] Show which values come from config file vs. session override vs. env var.
    - [ ] Red/Green TDD.
- [ ] Task: End-to-end test — set config, restart server, verify config persists.
- [ ] Task: Conductor - User Manual Verification 'System Config' (Protocol in workflow.md)

## Phase 2: Unified Binary

### Objective
Merge the MCP server and gRPC server into a single `cercano` binary with mode flags.

### Tasks
- [ ] Task: Create new `cmd/cercano/main.go` as the unified entry point.
    - [ ] Default mode: start gRPC server (replaces `cmd/agent/main.go`).
    - [ ] `--mcp` flag: start embedded gRPC server + MCP on stdio (replaces `cmd/mcp/main.go`).
    - [ ] Load system config on startup in both modes.
- [ ] Task: Refactor MCP server to optionally use in-process gRPC instead of network connection.
    - [ ] When `--mcp` is passed, create the gRPC server directly (no network listener needed).
    - [ ] The MCP handlers call the server methods directly instead of through a gRPC client.
    - [ ] Red/Green TDD.
- [ ] Task: Add subcommands: `cercano setup`, `cercano config`.
    - [ ] Use a CLI framework or simple flag/arg parsing.
- [ ] Task: Update Makefile.
    - [ ] `make build` — builds single `bin/cercano` binary.
    - [ ] `make dev` — build + kill old process + restart.
    - [ ] Keep `make test` and `make clean`.
- [ ] Task: Update `.mcp.json` / Claude Code config to use new binary path.
- [ ] Task: Remove old `cmd/agent/` and `cmd/mcp/` entry points.
- [ ] Task: End-to-end test — verify both modes work (standalone gRPC + embedded MCP).
- [ ] Task: Conductor - User Manual Verification 'Unified Binary' (Protocol in workflow.md)

## Phase 3: Dev Workflow & Setup Command

### Objective
Smooth the development loop and add a setup command for new users.

### Tasks
- [ ] Task: Implement `cercano setup` subcommand.
    - [ ] Check Ollama is running.
    - [ ] Check required models are pulled (nomic-embed-text, default local model).
    - [ ] Auto-pull missing models.
    - [ ] Print clear status/errors.
    - [ ] Red/Green TDD.
- [ ] Task: Test `make dev` workflow end-to-end.
    - [ ] Edit code → `make dev` → `/mcp` reconnect → config persists → test tool.
- [ ] Task: Update README Getting Started section.
    - [ ] "Quick Start" path: `cercano setup && cercano`
    - [ ] "With Claude Code" path: `claude mcp add cercano -- cercano --mcp`
    - [ ] "Developer" path: `make dev`
- [ ] Task: Conductor - User Manual Verification 'Dev Workflow & Setup' (Protocol in workflow.md)

## Phase 4: CI/CD Pipeline

### Objective
Automated testing on PRs and release binaries on tagged commits.

### Tasks
- [ ] Task: Create `.github/workflows/ci.yml`.
    - [ ] Trigger on push to `main` and on pull requests.
    - [ ] Run `go test ./...` in `source/server/`.
    - [ ] Build binary to verify compilation.
    - [ ] Cache Go modules.
- [ ] Task: Create `.github/workflows/release.yml`.
    - [ ] Trigger on pushed tags matching `v*`.
    - [ ] Build cross-platform binaries: macOS arm64, macOS amd64, Linux amd64.
    - [ ] Create GitHub Release with binaries attached.
- [ ] Task: Add version injection via `-ldflags` at build time.
    - [ ] `cercano --version` prints the version.
- [ ] Task: Test the full release workflow with a test tag.
- [ ] Task: Conductor - User Manual Verification 'CI/CD Pipeline' (Protocol in workflow.md)

## Phase 5: Homebrew Distribution

### Objective
Let macOS users install Cercano with `brew install`.

### Tasks
- [ ] Task: Create a Homebrew tap repo (`homebrew-cercano`).
- [ ] Task: Write the Homebrew formula.
    - [ ] Download release binary from GitHub Releases.
    - [ ] Install to `bin/cercano`.
    - [ ] Add caveats about Ollama dependency.
- [ ] Task: Test `brew install` → `cercano setup` → `cercano` end-to-end.
- [ ] Task: Update README with `brew install` instructions.
- [ ] Task: Conductor - User Manual Verification 'Homebrew Distribution' (Protocol in workflow.md)

## Phase 6 (Stretch): Docker

### Objective
Docker image for headless/LAN server deployments. Not blocking track completion.

### Tasks
- [ ] Task: Create multi-stage `Dockerfile` (golang build + alpine runtime).
- [ ] Task: Create `docker-compose.yml` with Ollama networking.
- [ ] Task: Verify `docker compose up` connects to host Ollama.
- [ ] Task: Conductor - User Manual Verification 'Docker' (Protocol in workflow.md)
