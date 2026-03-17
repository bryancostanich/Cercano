# Track Specification: Remote Inference

## 1. Job Title
Support runtime-configurable remote Ollama endpoints with model discovery and automatic fallback to local.

## 2. Overview
Cercano already supports pointing to a remote Ollama instance via the `OLLAMA_URL` environment variable at startup. This track makes that configuration **runtime-configurable**, adds **model discovery** so users can see what's available on the remote machine, and introduces a **fallback mechanism** so that if the remote endpoint becomes unavailable, Cercano automatically falls back to the local Ollama instance.

The primary use case is a developer with a powerful Mac Studio (or similar) on the LAN running Ollama with large models that their laptop can't run locally. Cercano should let them point at that machine, pick a model, and transparently fall back to local if the remote goes down.

**What changes:** The `OllamaProvider` gains a runtime-mutable `BaseURL` (thread-safe), the proto/gRPC interface adds an `ollama_url` field, the MCP `cercano_config` tool exposes it, and a new endpoint/tool lists available models on the active Ollama instance. A health-check loop monitors the remote endpoint and triggers fallback.

**What does NOT change:** The Ollama HTTP API contract, the SmartRouter, the agentic loop, or any IDE extension code. Non-Ollama backends (tiiny.ai, ONNX, etc.) are out of scope — those belong in the AI Engine Agnosticism track.

## 3. Architecture Decision

```
┌────────────────────────────────────────────────┐
│                 Cercano Server                  │
│                                                │
│  ┌──────────────┐    ┌──────────────────────┐  │
│  │ OllamaProvider│   │  Health Monitor       │  │
│  │              │    │  (goroutine)          │  │
│  │ primary: ────┼──► │  polls /api/tags      │  │
│  │   remote URL │    │  on failure → switch  │  │
│  │              │    │    to fallback URL    │  │
│  │ fallback: ───┼──► │  on recovery →       │  │
│  │   local URL  │    │    switch back        │  │
│  └──────────────┘    └──────────────────────┘  │
└────────┬──────────────────────┬────────────────┘
         │                      │
    ┌────┴────┐           ┌─────┴─────┐
    │ Remote  │           │  Local    │
    │ Ollama  │           │  Ollama   │
    │ (LAN)   │           │ (laptop)  │
    └─────────┘           └───────────┘
```

Key decisions:
- **Single active endpoint** — Cercano talks to one Ollama instance at a time, not load-balancing across multiple.
- **Primary + fallback** — The remote URL is "primary" and `localhost:11434` is always the implicit fallback. When the primary is unreachable, traffic automatically routes to fallback.
- **Health monitoring** — A background goroutine periodically pings the primary endpoint. On failure, it switches to fallback and logs a warning. On recovery, it switches back.
- **Model discovery via Ollama API** — Ollama's `GET /api/tags` returns all pulled models. Cercano exposes this so agents/users can see what's available and pick one.
- **No service discovery** — Users explicitly configure the remote URL. No mDNS/Bonjour scanning.

## 4. Requirements

### 4.1 Runtime-Configurable Ollama URL
- Add `ollama_url` field to `UpdateConfigRequest` in `agent.proto`.
- Update `Server.UpdateConfig()` to call a new `OllamaProvider.SetBaseURL()` method.
- Make `BaseURL` in `OllamaProvider` protected by the existing `sync.RWMutex`.
- Update `cercano_config` MCP tool to accept and pass through `ollama_url`.
- Validate the URL format before accepting (must be a valid HTTP/HTTPS URL).

### 4.2 Model Discovery
- New gRPC RPC: `ListModels` — queries the active Ollama instance's `GET /api/tags` and returns the list of available models.
- New MCP tool: `cercano_models` — exposes `ListModels` to MCP clients. Returns model names, sizes, and modification dates.
- This lets an agent or user query what's available on the remote machine before switching models.

### 4.3 Fallback Mechanism
- `OllamaProvider` stores two URLs: `primaryURL` (user-configured remote) and `fallbackURL` (always `localhost:11434`).
- When only local is configured (no remote), there is no fallback — it's just the single local endpoint.
- A background health monitor goroutine:
  - Pings the primary URL every 30 seconds (configurable) via `GET /api/tags` (lightweight, also validates Ollama is functional).
  - On failure (3 consecutive failures): logs a warning, switches `activeURL` to fallback.
  - On recovery (primary responds again): logs info, switches `activeURL` back to primary.
- All `Process`/`ProcessStream` calls use `activeURL`, not `primaryURL` directly.
- Fallback state is reported in response metadata so the caller knows which endpoint served the request.

### 4.4 Configuration Flow
```
User sets remote URL via cercano_config
  → OllamaProvider.SetBaseURL(remoteURL)
  → primaryURL = remoteURL, fallbackURL = localhost:11434
  → health monitor starts pinging primaryURL
  → if primary healthy: activeURL = primaryURL
  → if primary down:    activeURL = fallbackURL
  → response metadata includes: [Endpoint: remote|local(fallback)]
```

### 4.5 Observability
- Log when switching between primary and fallback (and back).
- Include active endpoint info in response metadata (already includes model name and confidence).
- `cercano_config` with `action: "get"` (when implemented) should report the active endpoint and fallback status.

## 5. Acceptance Criteria
- [ ] `cercano_config(action: "set", ollama_url: "http://mac-studio.local:11434")` switches the active endpoint at runtime.
- [ ] `cercano_models` returns the list of models available on the active endpoint.
- [ ] When the remote endpoint goes down, requests automatically route to local Ollama.
- [ ] When the remote endpoint recovers, requests automatically route back to it.
- [ ] Response metadata indicates which endpoint served the request.
- [ ] Existing behavior is unchanged when no remote URL is configured (local-only mode).

## 6. Out of Scope
- Multiple simultaneous Ollama endpoints / load balancing.
- Service discovery (mDNS, Bonjour, network scanning).
- Non-Ollama inference backends (tiiny.ai, ONNX Runtime, etc.) — see AI Engine Agnosticism track.
- GPU/hardware capability detection on the remote machine (beyond what Ollama's API exposes).
- Authentication for remote Ollama instances (Ollama doesn't support auth natively).
