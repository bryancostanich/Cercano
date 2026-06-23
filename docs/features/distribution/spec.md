# User-Friendly Distribution

## Overview

This feature makes Cercano a single binary that is easy to install, configure, develop against, and distribute. Previously Cercano required building two binaries from source (`bin/agent` and `bin/cercano-mcp`), manually starting the gRPC server in a separate terminal, and reconfiguring settings (such as a remote Ollama URL) on every restart. The work unifies the MCP and gRPC servers into one `cercano` binary with mode flags, adds a persistent system config file so settings survive restarts, smooths the developer loop with `make dev`, adds a `cercano setup` command, ships CI/CD pipelines for testing and cross-platform releases, and provides Homebrew installation for macOS. It addresses three personas: end users (`brew install`, Claude Code auto-launch, persistent config), developers (`make dev` one-command rebuild/restart), and LAN/server deployments. The gRPC interface, MCP tool surface, SmartRouter, and agentic loop are unchanged, and Ollama stays on the host (GPU/Metal passthrough constraints).

## Design / Architecture

**Unified binary.** A new `cmd/cercano/main.go` is the single entry point, replacing `bin/agent` and `bin/cercano-mcp`. Modes:
- `cercano` — default; starts the gRPC server on port 50052.
- `cercano --mcp` — embedded mode; starts a gRPC server in-process on a random port (localhost:0) in a goroutine, with MCP served on stdio and handlers connecting via a standard gRPC client. No manual server management.
- `cercano --mcp --grpc-addr host:port` — connect MCP to an external gRPC server.
- `cercano --version` — print the version (injected at build time via `-ldflags`).
- `cercano setup`, `cercano config` — subcommands.

System config is loaded on startup in both modes.

**System config.** A new `internal/config/` package provides Load/Save/Defaults against `~/.config/cercano/config.yaml`. Fields: `ollama_url`, `local_model`, `embedding_model`, `cloud_provider`, `cloud_model`, `cloud_api_key`, `port`. Precedence (lowest to highest): defaults → config file → environment variables → (and at runtime) session `cercano_config` changes and CLI flags. `GEMINI_API_KEY` auto-sets cloud provider/model defaults. Missing files fall back to defaults. This is what lets the Mac Studio Ollama URL and chosen model persist across restarts instead of being re-entered each time.

**Dev workflow.** The Makefile builds a single `bin/cercano`. `make build` compiles only; `make dev` builds, kills the old process, and restarts in one step. Legacy `make agent` / `make mcp` targets still work during the transition.

**Setup command.** `cercano setup` checks that Ollama is running, checks that required models are pulled (`nomic-embed-text` and the default local model), auto-pulls missing models, creates a default config file if none exists, and prints clear status and actionable errors.

**CI/CD.** `.github/workflows/ci.yml` triggers on push to `main` and on PRs, runs `go test ./...` in `source/server/`, builds the binary to verify compilation, and caches Go modules. `.github/workflows/release.yml` triggers on `v*` tags, builds cross-platform binaries (macOS arm64, macOS amd64, Linux amd64), and creates a GitHub Release with binaries attached. Version is injected via `-ldflags`.

**Homebrew.** A Homebrew tap (`homebrew-cercano`) hosts the formula (`source/server/Formula/cercano.rb`). The formula installs to `bin/cercano` and carries caveats about the Ollama dependency. It initially built from source via `go build` with ldflags version injection, then switched to downloading the GitHub Release binary once the repo went public. A local tap (`cercano/local`) was used for testing.

**Architecture.** Each client (Claude Code, VS Code, Cursor) runs its own lightweight `cercano` process; the LLM is the heavy resource and Ollama handles concurrent requests on the host. `cercano_config` changes are session-scoped; persistent changes go to the system config file.

## Key behaviors / capabilities

- One `cercano` binary serving both standalone gRPC and embedded MCP+gRPC modes.
- Persistent system config surviving restarts, with a clear env > file > defaults precedence.
- `make dev` one-command rebuild + restart; `make build` for build-only.
- `cercano setup` validates prerequisites and auto-pulls models.
- `brew install` path for macOS via a dedicated tap.
- CI on every PR/push; tagged commits produce cross-platform release binaries.
- Existing VS Code and MCP clients connect unchanged.

## Notable decisions / constraints

- Ollama stays on the host (no containerization) due to GPU/Metal passthrough constraints.
- Multiple clients each run their own lightweight process rather than sharing one server.
- Out of scope: Ollama containerization, Kubernetes, multi-architecture Docker images, and Windows support.
- The Docker work was split out into its own track (`conductor/tracks/docker_20260320/`).

## Remaining / not-yet-done

- `cercano_config(action: "get")` deferred — requires a new gRPC RPC.
- Update `.mcp.json` / Claude Code config to point at the new binary path.
- Remove the old `cmd/agent/` and `cmd/mcp/` entry points after the transition period.
- README updates: Getting Started (Quick Start `cercano setup && cercano`, Claude Code `claude mcp add cercano -- cercano --mcp`, Developer `make dev`) and `brew install` instructions (the latter partially done).
- Conductor user-manual verification for the Homebrew phase remains unchecked.
