# Track Specification: AI Engine Bootstrapping

## 1. Job Title
Make `cercano setup` detect missing AI engine backends and offer to install one, with Ollama as the recommended default.

## 2. Overview
Currently, `cercano setup` assumes Ollama is already installed and running. If it's not, setup prints an error and exits. This is a poor first-run experience — new users hit a wall and have to figure out Ollama installation on their own.

This track adds engine detection and guided installation to `cercano setup`. The framing is engine-agnostic (Cercano's architecture supports pluggable backends), but Ollama is recommended as the simplest path today.

### User Experience

**First run, no engine installed:**
```
Cercano Setup (v0.6.1)
Checking prerequisites...

[1/6] Checking for AI engine backends...
  No AI engine backend was detected.
  Would you like help installing one?
  Ollama is recommended as the simplest path.

  Install Ollama now? [Y/n]: y

  Installing Ollama via Homebrew...
  OK: Ollama installed.
  Starting Ollama...
  OK: Ollama is running.

[2/6] Checking required models...
  ...
```

**Scripted/CI usage:**
```bash
cercano setup --install-engine    # auto-install without prompting
```

**Engine already present:**
```
[1/6] Checking for AI engine backends...
  OK: Ollama is running at http://localhost:11434
```

### What Changes
- `cercano setup` gains a new first step: engine detection
- Interactive yes/no prompt when no engine is found (default: yes)
- `--install-engine` flag to skip the prompt for scripted use
- Platform-aware install commands (macOS via Homebrew, Linux via Ollama's install script)
- Ollama is auto-started after installation if not already running
- Step numbering changes from [1/5] to [1/6]

### What Does NOT Change
- The engine abstraction layer (InferenceEngine, EngineRegistry)
- Existing model pull, config, hook, and venv steps
- Behavior when Ollama is already installed and running
- The Homebrew formula (Ollama remains a caveat, not a hard dependency)

## 3. Architecture Decision

### Engine Detection
Detection checks whether any supported engine is reachable, not just Ollama. Today that's only Ollama (via `GET /api/tags`), but the design should accommodate future engines:

```go
type EngineDetector struct {
    // checks is a list of engine detection functions
    // Each returns (engineName, isAvailable)
}
```

For now, a simple Ollama HTTP check is sufficient. The detector can be extended when new engines are added.

### Installation Strategy by Platform

| Platform | Method | Command |
|----------|--------|---------|
| macOS | Homebrew | `brew install ollama` |
| Linux | Ollama installer | `curl -fsSL https://ollama.com/install.sh \| sh` |

If Homebrew is not available on macOS, fall back to printing the download URL.

### Post-Install Start
After installing Ollama, the setup command should attempt to start it:
- macOS: `brew services start ollama` or `ollama serve &`
- Linux: Check if systemd service was created, otherwise `ollama serve &`
- Wait briefly (up to 10s) for Ollama to become responsive before continuing

### Interactive vs Non-Interactive
- By default, `cercano setup` prompts the user with `Install Ollama now? [Y/n]:`
- The `--install-engine` flag pre-answers "yes" — useful for CI, Docker, or scripted installs
- If stdin is not a terminal (piped input), treat as non-interactive and print guidance instead of prompting

## 4. Requirements

### 4.1 Engine Detection
- Check if any supported engine backend is reachable before proceeding with setup
- Engine-agnostic messaging: "No AI engine backend was detected"
- Recommend Ollama: "Ollama is recommended as the simplest path"
- If engine is found, print which engine and URL, then continue

### 4.2 Interactive Installation Prompt
- Prompt: `Install Ollama now? [Y/n]:`
- Default to yes (empty input = yes)
- On "no", print manual install instructions and continue setup (skip model pull steps)
- On non-TTY stdin, print instructions without prompting

### 4.3 `--install-engine` Flag
- Skips the interactive prompt, proceeds directly to installation
- Works with `cercano setup --install-engine`
- Should also work as a subcommand flag, not a global flag

### 4.4 Platform-Aware Installation
- Detect macOS vs Linux via `runtime.GOOS`
- macOS: prefer `brew install ollama` if Homebrew is available
- macOS fallback: print download URL (https://ollama.com/download)
- Linux: run `curl -fsSL https://ollama.com/install.sh | sh`
- Print clear progress and error messages during installation

### 4.5 Post-Install Engine Start
- After successful install, check if Ollama is already running
- If not, start it and wait (with timeout) for it to become responsive
- Verify with the existing `checkOllama()` health check

## 5. Acceptance Criteria
- [ ] `cercano setup` detects when no engine is installed and offers to install Ollama
- [ ] Interactive prompt defaults to yes, accepts y/n/Y/N
- [ ] `cercano setup --install-engine` installs without prompting
- [ ] Installation works on macOS (via Homebrew) and Linux (via installer script)
- [ ] Ollama is started after installation and verified responsive
- [ ] When engine is already running, setup proceeds as before with no behavior change
- [ ] Non-TTY stdin prints guidance without hanging on a prompt
- [ ] Messaging is engine-agnostic ("No AI engine backend was detected")

## 6. Out of Scope
- Installing engines other than Ollama (future — when new engines are added)
- Windows support
- Ollama as a hard Homebrew dependency (keep as caveat only)
- Automatic engine selection when multiple engines are available
