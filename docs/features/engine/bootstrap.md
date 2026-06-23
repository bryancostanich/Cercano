# AI Engine Bootstrapping

## Overview

`cercano setup` detects whether an AI engine backend is available and, if none is found, offers to install one — with Ollama recommended as the simplest active inference path. Previously setup assumed Ollama was already installed and running, printing an error and exiting otherwise, which left new users to figure out Ollama installation themselves. This feature adds a guided first-run experience: a new initial setup step performs engine detection, prompts the user (interactively or via flag), installs Ollama using a platform-appropriate method, starts it, and verifies it is responsive before continuing with the model-pull, config, hook, and venv steps. Setup pulls the selected chat model and configured embedding model, and also prepares the optional managed `llama-server` runtime used by embedded inference. The messaging is engine-agnostic ("No AI engine backend was detected") since Cercano's architecture supports pluggable backends; Ollama is just the recommended default today.

## Design / Architecture

The logic lives in `cmd/cercano/main.go` and integrates into the existing `runSetup()` flow.

**Engine detection.** The Ollama health check (`GET /api/tags`, via the existing `checkOllama()`) was extracted into a reusable `detectEngineWith()` that returns engine name + availability. An `engineCheckFunc` type makes the detector extensible so additional engines can be plugged in later; today only Ollama is checked.

**Interactive vs non-interactive.** When no engine is detected, setup prints the engine-agnostic message and prompts `Install Ollama now? [Y/n]:`, defaulting to yes (empty input = yes). A `parseYesNo` helper accepts y/n/Y/N. On "no", manual install instructions are printed and setup continues (model-pull steps are skipped, since they would fail without an engine). When stdin is not a terminal (nil/piped reader), guidance is printed without prompting so scripted runs don't hang. The `--install-engine` flag (a subcommand flag on `cercano setup`, not a global flag) pre-answers "yes" for CI/Docker/scripted installs.

**Platform-aware installation.** `runtime.GOOS` dispatches the install path. On macOS, `exec.LookPath("brew")` checks for Homebrew; if present, `brew install ollama` runs with output streamed live; if absent, an empty command is returned and the caller prints the download URL (https://ollama.com/download). On Linux, `curl -fsSL https://ollama.com/install.sh | sh` runs with streamed output. Unsupported platforms return an empty command so the caller prints the manual URL.

**Post-install start.** After a successful install, `checkOllama()` determines whether Ollama is already running. If not: on macOS, `brew services start ollama` is tried, falling back to `ollama serve` in the background; on Linux, `ollama serve` in the background. Setup then polls `checkOllama()` once per second for up to 10 seconds for the engine to become responsive, printing "Starting Ollama..." then "OK: Ollama is running." If install or start fails, an actionable error is printed and setup exits.

**Integration.** Engine detection becomes the new first step; step numbering shifted from [1/5] to [1/6] with subsequent steps renumbered. When the engine is already present, setup prints which engine and URL ("OK: Ollama is running at http://localhost:11434") and proceeds exactly as before with no behavior change. After a successful install + start, setup continues into the existing model-pull and config steps.

**Managed runtime setup.** Setup now also prepares `llama-server` from `llama.cpp`: it creates the configured GGUF model directories, detects or installs `llama-server` through Homebrew when available, enables `llama_server.enabled` when the binary exists, and records a default GGUF model when exactly one configured model is found. This does not change `local_runtime` away from `ollama`; the managed sidecar is available for runtime inventory, dashboard, and explicit start/stop flows.

## Key behaviors / capabilities

- Detects a missing engine backend and offers guided installation.
- Interactive prompt defaults to yes; accepts y/n/Y/N.
- `--install-engine` flag installs without prompting (scripted/CI use).
- Platform-aware install: macOS via Homebrew (download-URL fallback), Linux via Ollama installer script.
- Auto-starts Ollama post-install and polls up to 10s for responsiveness.
- Automatically pulls the configured embedding model (`nomic-embed-text` by default) when Ollama is available.
- Prepares optional managed `llama-server` runtime and `~/.cercano/models`.
- Non-TTY stdin prints guidance instead of hanging on a prompt.
- Engine-agnostic messaging; existing already-running flow unchanged.

## Notable decisions / constraints

- Detection is engine-agnostic by design (`engineCheckFunc`), though only Ollama is wired today.
- Ollama remains a Homebrew caveat, not a hard dependency.
- Out of scope: replacing Ollama as the default active inference engine, Windows runtime automation, and automatic selection when multiple GGUF models are present.

## Remaining / not-yet-done

- End-to-end test on a clean macOS machine (no Ollama installed) to exercise the full detect → install → start → continue flow.
- README Getting Started update noting that `cercano setup` handles Ollama installation.
- Conductor user-manual verification steps for all three phases remain unchecked.
