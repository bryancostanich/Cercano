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

## Phase 1: Server Process Management [complete]

### Objective
Extension spawns and manages the Go server as a child process.

### Tasks
- [x] Task: Locate pre-built server binary on extension activation.
    - [x] Convention: `source/server/bin/agent` resolved relative to extension path via `resolveServerBinaryPath()`.
- [x] Task: Spawn the server process from `extension.ts` on activation.
    - [x] `ServerManager` class using `child_process.spawn` with stdout/stderr piped to "Cercano Server" output channel.
    - [x] Waits for server readiness by parsing stdout for "Server listening at" pattern (30s timeout).
- [x] Task: Kill the server process on extension deactivation.
    - [x] `deactivate()` calls `serverManager.stop()` — SIGTERM with 3s fallback to SIGKILL.
    - [x] `dispose()` registered on `context.subscriptions` as safety net.
- [x] Task: Handle edge cases.
    - [x] Server already running (port in use) — `checkPortInUse()` detects and reuses.
    - [x] Server crashes mid-session — notifies user via warning message.
    - [x] Spawn failure or timeout — shows error message, extension continues (degraded).
- [x] Task: Conductor - User Manual Verification

*Files: `serverHelpers.ts` (pure helpers), `serverManager.ts` (ServerManager class), `serverManager.test.ts` (8 tests), `extension.ts` (integration).*

## Phase 2: Configuration [complete]

### Objective
Make the server launch configurable for development vs production workflows.

### Tasks
- [x] Task: Add extension settings to `package.json`.
    - [x] `cercano.server.autoLaunch` (boolean, default true) — toggle auto-launch.
    - [x] `cercano.server.binaryPath` (string, optional) — override path to server binary.
    - [x] `cercano.server.port` (number, default 50052) — configurable gRPC port.
- [x] Task: Pass port configuration to both server and client.
    - [x] Extension passes `CERCANO_PORT` env var to spawned server process.
    - [x] Extension passes port to `CercanoClient` constructor.
    - [x] Go server reads `CERCANO_PORT` env var, falls back to `50052`.
- [x] Task: Show server status in VS Code status bar (running/stopped/error).
    - [x] Right-aligned status bar item with codicon states: check (running), circle-slash (stopped), sync~spin (starting), error (error).
    - [x] Clicking opens the config menu.
- [x] Task: Changed activation event from `onChatParticipant` to `onStartupFinished` so server and status bar are available immediately.
- [x] Task: Conductor - User Manual Verification

*Files: `package.json` (settings + activation), `serverManager.ts` (config, status bar, env var), `extension.ts` (reads config, passes port to client), `cmd/agent/main.go` (reads CERCANO_PORT).*

## Phase 3: Ollama Dependency Check

### Objective
Verify Ollama is running before starting the server, with helpful error messages.

### Tasks
- [ ] Task: Check if Ollama is reachable on activation (before spawning server).
    - [ ] If not running, show actionable error message with link to install instructions.
- [ ] Task: Add `cercano.ollama.url` setting (default `http://localhost:11434`).
- [ ] Task: Pass Ollama URL to server via env var or CLI flag.
- [ ] Task: Conductor - User Manual Verification
