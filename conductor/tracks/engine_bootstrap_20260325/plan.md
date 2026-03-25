# Track Plan: AI Engine Bootstrapping

## Phase 1: Engine Detection & Interactive Prompt

### Objective
Detect whether an AI engine backend is available and prompt the user to install one if not.

### Tasks
- [x] Task: Add engine detection logic to `cmd/cercano/main.go`.
    - [x] Extract Ollama health check into a reusable `detectEngineWith()` function that returns engine name + status.
    - [x] Design for extensibility: engineCheckFunc type allows plugging in new engine checks.
    - [x] Red/Green TDD: TestDetectEngine_Reachable, TestDetectEngine_Unreachable.
- [x] Task: Add interactive prompt when no engine is detected.
    - [x] Print engine-agnostic message: "No AI engine backend was detected..."
    - [x] Prompt: `Install Ollama now? [Y/n]:` — default yes.
    - [x] On "no", print manual install instructions and continue setup (skip model pull).
    - [x] On non-TTY stdin (nil reader), print instructions without prompting.
    - [x] Red/Green TDD: TestParseYesNo, TestPromptInstall_AutoYes/Interactive/Declined.
- [x] Task: Add `--install-engine` flag to `cercano setup`.
    - [x] Parse flag before entering setup flow.
    - [x] When set, skip prompt and proceed directly to install.
    - [x] Red/Green TDD: TestPromptInstall_AutoYes verifies flag behavior.
- [x] Task: Update step numbering from [1/5] to [1/6] and shift subsequent steps.
- [ ] Task: Conductor - User Manual Verification 'Engine Detection & Prompt' (Protocol in workflow.md)

## Phase 2: Platform-Aware Installation

### Objective
Install Ollama automatically based on the user's platform.

### Tasks
- [x] Task: Implement macOS installation path.
    - [x] Check for Homebrew via `exec.LookPath("brew")`.
    - [x] If Homebrew available: run `brew install ollama`.
    - [x] If Homebrew not available: return empty command (caller prints download URL).
    - [x] Stream install output to the user in real time.
    - [x] Red/Green TDD: TestInstallCommand_Darwin, TestInstallCommand_DarwinNoBrew.
- [x] Task: Implement Linux installation path.
    - [x] Run `curl -fsSL https://ollama.com/install.sh | sh`.
    - [x] Stream output to the user.
    - [x] Red/Green TDD: TestInstallCommand_Linux.
- [x] Task: Implement platform detection and dispatch.
    - [x] Use `runtime.GOOS` to select macOS vs Linux path.
    - [x] On unsupported platforms, return empty command (caller prints manual URL).
    - [x] Red/Green TDD: TestInstallCommand_Unsupported, TestPlatformDetection.
- [ ] Task: Conductor - User Manual Verification 'Platform-Aware Installation' (Protocol in workflow.md)

## Phase 3: Post-Install Start & Integration

### Objective
Start the engine after installation and verify it's responsive before continuing setup.

### Tasks
- [x] Task: Implement post-install engine start.
    - [x] After successful install, check if Ollama is already running via `checkOllama()`.
    - [x] If not running on macOS: try `brew services start ollama`, fall back to `ollama serve` in background.
    - [x] If not running on Linux: `ollama serve` in background.
    - [x] Wait up to 10 seconds for engine to become responsive (poll `checkOllama()` every second).
    - [x] Print clear status: "Starting Ollama..." / "OK: Ollama is running."
    - [x] Red/Green TDD: TestWaitForEngine_AlreadyRunning, _BecomesAvailable, _Timeout. TestStartCommand_Darwin, _Linux.
- [x] Task: Integrate into `runSetup()` flow.
    - [x] After successful engine start, continue with existing model pull and config steps.
    - [x] If user declined install, skip model pull steps (they'll fail without an engine).
    - [x] If install or start fails, print actionable error and exit.
- [ ] Task: End-to-end test on macOS — run `cercano setup` with no Ollama installed, verify full flow.
- [ ] Task: Update README Getting Started section to mention that `cercano setup` handles Ollama installation.
- [ ] Task: Conductor - User Manual Verification 'Post-Install Start & Integration' (Protocol in workflow.md)
