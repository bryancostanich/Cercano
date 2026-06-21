# Automatic Server Launch

## Overview

Previously, users had to manually build and run the Cercano Go server in a separate terminal before the VS Code extension would work. This feature makes the extension own the full server lifecycle — it spawns the Go server as a child process on activation and terminates it on deactivation, with configuration, a status indicator, and an Ollama pre-flight check.

## Design / Architecture

The lifecycle is owned by a `ServerManager` class in the extension, supported by pure helpers in `serverHelpers.ts` (unit-tested in `serverManager.test.ts`).

- **Binary resolution** — `resolveServerBinaryPath()` locates the pre-built binary by convention at `source/server/bin/agent`, resolved relative to the extension path; overridable via setting.
- **Spawn** — `ServerManager` uses `child_process.spawn`, pipes stdout/stderr to a "Cercano Server" output channel, and waits for readiness by parsing stdout for the `Server listening at` pattern (30s timeout).
- **Shutdown** — `deactivate()` calls `serverManager.stop()` (SIGTERM with a 3s fallback to SIGKILL); `dispose()` is also registered on `context.subscriptions` as a safety net.
- **Activation timing** — the activation event was changed from `onChatParticipant` to `onStartupFinished` so the server and status bar are available immediately.
- **Dev inner loop** — a build-only `Build Server` task in `.vscode/tasks.json` and a `Run Extension` compound launch config (`stopAll: true`); an `Extension Only` config remains for running the server manually. (An earlier `Build & Run Server` background task was superseded once `ServerManager` took over the lifecycle, which also fixed orphaned-process issues.)

## Key Behaviors / Capabilities

- Server auto-starts on extension activation and stops on deactivation.
- **Edge cases handled:** port already in use (`checkPortInUse()` detects and reuses an existing server), server crash mid-session (warning notification), and spawn failure/timeout (error message; extension continues in a degraded mode).
- **Ollama pre-flight check** — `checkOllamaReachable()` does an HTTP GET to `/api/tags` (3s timeout) before spawning the server; if Ollama is unreachable it shows an error dialog with "Download Ollama" and "Open Settings" actions and sets the status bar to an error state.
- **Status bar item** (right-aligned) reflects server state via codicons: check (running), circle-slash (stopped), sync~spin (starting), error (error); clicking opens the config menu.
- **Settings** (`package.json`):
  - `cercano.server.autoLaunch` (boolean, default true)
  - `cercano.server.binaryPath` (string, optional override)
  - `cercano.server.port` (number, default 50052)
  - `cercano.ollama.url` (string, default `http://localhost:11434`)
- **Configuration plumbing** — the extension passes `CERCANO_PORT` and `OLLAMA_URL` env vars to the spawned server (Go server reads them, falling back to `50052` and `http://localhost:11434`) and passes the port to the `CercanoClient` constructor.

## Notable Decisions / Constraints

- `ServerManager` is the single owner of the server process; the dev launch config builds only and does not run the server, eliminating orphaned processes.
- The `$go` problem matcher was removed from the build task because it misinterpreted shell-profile warnings as errors.
- If the server or Ollama is unavailable, the extension degrades rather than failing hard.
