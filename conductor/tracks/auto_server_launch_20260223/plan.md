# Track Plan: Automatic Server Launch

The VS Code extension should automatically manage the Cercano server lifecycle — start it when the extension activates and stop it when it deactivates. Today, users must manually build and run the server in a separate terminal before using the extension.

## Phase 0: Developer Workflow (VS Code Launch Config) [complete]

### Objective
Streamline the development inner loop so F5 builds and runs the server automatically.

### Tasks
- [x] Task: Add `Build & Run Server` background task to `.vscode/tasks.json`.
    - [x] Kills any existing process on port 50052 before starting.
    - [x] Runs `go build -o bin/agent ./cmd/agent && ./bin/agent` from `source/server/`.
    - [x] Custom background problem matcher: `beginsPattern` on "Starting Cercano", `endsPattern` on "Server listening at" — VS Code knows the server is ready.
- [x] Task: Add compound launch configuration `Run Extension` to `.vscode/launch.json`.
    - [x] Runs `Build & Run Server` as `preLaunchTask`, then launches extension host.
    - [x] `stopAll: true` to terminate on debug stop.
    - [x] Kept `Extension Only` config for cases where server is run manually.
- [x] Task: Conductor - User Manual Verification

*Notes: The server process is orphaned when debugging stops (VS Code limitation with background tasks). The `kill` command at the start of the task is the self-cleaning mechanism — each F5 press guarantees a fresh server. Phase 1 will solve this properly with extension-managed child process lifecycle.*

## Phase 1: Server Process Management

### Objective
Extension spawns and manages the Go server as a child process.

### Tasks
- [ ] Task: Build the server binary as part of extension activation (or locate a pre-built binary).
    - [ ] Define a convention for where the binary lives (e.g., `bin/agent` relative to workspace root, or bundled with extension).
- [ ] Task: Spawn the server process from `extension.ts` on activation.
    - [ ] Use `child_process.spawn` with stdout/stderr piped to an output channel.
    - [ ] Wait for the server to be ready (poll the gRPC port or parse stdout for "listening" message).
- [ ] Task: Kill the server process on extension deactivation.
    - [ ] Handle graceful shutdown (SIGTERM) with a fallback to SIGKILL.
- [ ] Task: Handle edge cases.
    - [ ] Server already running (port in use) — detect and reuse.
    - [ ] Server crashes mid-session — notify user, optionally restart.
- [ ] Task: Conductor - User Manual Verification

## Phase 2: Configuration

### Objective
Make the server launch configurable for development vs production workflows.

### Tasks
- [ ] Task: Add extension settings.
    - [ ] `cercano.server.autoLaunch` (boolean, default true) — toggle auto-launch.
    - [ ] `cercano.server.binaryPath` (string, optional) — override path to server binary.
    - [ ] `cercano.server.port` (number, default 50052) — configurable gRPC port.
- [ ] Task: Pass port configuration to both server (CLI flag or env var) and client.
- [ ] Task: Show server status in VS Code status bar (running/stopped/error).
- [ ] Task: Conductor - User Manual Verification

## Phase 3: Ollama Dependency Check

### Objective
Verify Ollama is running before starting the server, with helpful error messages.

### Tasks
- [ ] Task: Check if Ollama is reachable on activation (before spawning server).
    - [ ] If not running, show actionable error message with link to install instructions.
- [ ] Task: Add `cercano.ollama.url` setting (default `http://localhost:11434`).
- [ ] Task: Pass Ollama URL to server via env var or CLI flag.
- [ ] Task: Conductor - User Manual Verification
