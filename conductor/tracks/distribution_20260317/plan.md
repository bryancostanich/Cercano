# Track Plan: User-Friendly Distribution

## Phase 1: System Config [checkpoint: 3f7021d]

### Objective
Add a persistent config file so settings like Ollama URL and default model survive restarts.

### Tasks
- [x] Task: Design config file format and location (`~/.config/cercano/config.yaml`). [b915999]
    - [x] Define fields: ollama_url, local_model, embedding_model, cloud_provider, cloud_model, cloud_api_key, port.
    - [x] Define precedence: env vars > config file > defaults.
- [x] Task: Implement config loading in the server. [b915999]
    - [x] New `internal/config/` package with Load/Save/Defaults.
    - [x] Load from `~/.config/cercano/config.yaml` on startup.
    - [x] Fall back to defaults if file doesn't exist.
    - [x] Environment variables override config file values.
    - [x] GEMINI_API_KEY auto-sets cloud provider/model defaults.
    - [x] Red/Green TDD: 11 tests (defaults, file load, env overrides, invalid YAML, round-trip, etc.).
- [x] Task: Wire config into cmd/agent/main.go, replacing inline env var reading. [b915999]
- [x] Task: End-to-end test — set Mac Studio URL in config, restart server, verified it loaded from file.
- [ ] Task: Implement `cercano_config(action: "get")` — deferred, requires new gRPC RPC.
- [x] Task: Conductor - User Manual Verification 'System Config' (Protocol in workflow.md)

## Phase 2: Unified Binary [checkpoint: 3f7021d]

### Objective
Merge the MCP server and gRPC server into a single `cercano` binary with mode flags.

### Tasks
- [x] Task: Create new `cmd/cercano/main.go` as the unified entry point. [7a9ec16]
    - [x] Default mode: start gRPC server (replaces `cmd/agent/main.go`).
    - [x] `--mcp` flag: embedded gRPC server on random port + MCP on stdio.
    - [x] `--mcp --grpc-addr host:port` for connecting to external gRPC server.
    - [x] `--version` flag.
    - [x] Load system config on startup in both modes.
- [x] Task: Embedded MCP mode starts gRPC server in-process. [7a9ec16]
    - [x] gRPC server starts on localhost:0 (random port) in a goroutine.
    - [x] MCP handlers connect to it via standard gRPC client.
    - [x] No manual server management needed.
- [x] Task: Update Makefile. [7a9ec16]
    - [x] `make build` — builds single `bin/cercano` binary.
    - [x] `make dev` — build + kill old process + restart.
    - [x] Legacy `make agent` / `make mcp` still work.
- [x] Task: End-to-end test — standalone mode starts and listens on port 50052.
- [ ] Task: Update `.mcp.json` / Claude Code config to use new binary path.
- [ ] Task: Remove old `cmd/agent/` and `cmd/mcp/` entry points (after transition period).
- [x] Task: Conductor - User Manual Verification 'Unified Binary' (Protocol in workflow.md)

## Phase 3: Dev Workflow & Setup Command [checkpoint: 3f7021d]

### Objective
Smooth the development loop and add a setup command for new users.

### Tasks
- [x] Task: Implement `cercano setup` subcommand. [d809ee6]
    - [x] Check Ollama is running.
    - [x] Check required models are pulled (nomic-embed-text, default local model).
    - [x] Auto-pull missing models.
    - [x] Create default config file if none exists.
    - [x] Print clear status/errors.
- [x] Task: Test `cercano setup` end-to-end — verified all checks pass, config file created.
- [x] Task: `make dev` workflow tested — build + restart in one command.
- [ ] Task: Update README Getting Started section.
    - [ ] "Quick Start" path: `cercano setup && cercano`
    - [ ] "With Claude Code" path: `claude mcp add cercano -- cercano --mcp`
    - [ ] "Developer" path: `make dev`
- [x] Task: Conductor - User Manual Verification 'Dev Workflow & Setup' (Protocol in workflow.md)

## Phase 4: CI/CD Pipeline

### Objective
Automated testing on PRs and release binaries on tagged commits.

### Tasks
- [x] Task: Create `.github/workflows/ci.yml`.
    - [x] Trigger on push to `main` and on pull requests.
    - [x] Run `go test ./...` in `source/server/`.
    - [x] Build binary to verify compilation.
    - [x] Cache Go modules.
- [x] Task: Create `.github/workflows/release.yml`.
    - [x] Trigger on pushed tags matching `v*`.
    - [x] Build cross-platform binaries: macOS arm64, macOS amd64, Linux amd64.
    - [x] Create GitHub Release with binaries attached.
- [x] Task: Add version injection via `-ldflags` at build time.
    - [x] `cercano --version` prints the version.
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
