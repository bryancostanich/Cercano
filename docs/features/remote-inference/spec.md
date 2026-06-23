# Remote Inference

## Overview
Cercano supports runtime-configurable remote Ollama endpoints with model discovery and automatic fallback to local. Previously the Ollama endpoint could only be set via the `OLLAMA_URL` environment variable at startup. This feature makes the endpoint changeable at runtime, lets users discover which models are available on the remote machine, and transparently falls back to the local Ollama instance if the remote becomes unreachable. The primary use case: a developer with a powerful LAN machine (e.g. a Mac Studio) running large models their laptop can't, who points Cercano at that machine and gets automatic failover to local when it goes down.

## Design / Architecture
The `OllamaProvider` was extended with primary/fallback URL management and a background health monitor.

```
┌─ Cercano Server ───────────────────────────────┐
│  OllamaProvider          Health Monitor (goroutine)
│   primaryURL  ──────────► polls /api/tags
│   fallbackURL                on failure → activeURL = fallback
│   activeURL                  on recovery → activeURL = primary
└────────┬───────────────────────┬───────────────┘
    Remote Ollama (LAN)      Local Ollama (laptop, localhost:11434)
```

Key decisions: single active endpoint at a time (no load-balancing across multiple instances); primary + implicit fallback (`localhost:11434` is always the fallback when a remote primary is set); health monitoring via a background goroutine; model discovery through Ollama's native `GET /api/tags`; and no service discovery — users explicitly configure the remote URL (no mDNS/Bonjour scanning).

`BaseURL` was made thread-safe behind the existing `sync.RWMutex` (both `ModelName` and URL now read under lock in `Process`/`ProcessStream`). An `ollama_url` field was added to `UpdateConfigRequest` in `agent.proto` (with regenerated Go bindings) and `Server.UpdateConfig()` calls `OllamaProvider.SetBaseURL()` after validating the URL is a valid HTTP/HTTPS URL.

## Key behaviors / capabilities
- **Runtime URL switching** — `cercano_config(action: "set", ollama_url: "http://mac-studio.local:11434")` changes the active endpoint live; the next query hits the new endpoint.
- **Model discovery** — new `ListModels` gRPC RPC queries the active instance's `GET /api/tags`; exposed via the new `cercano_models` MCP tool returning model names, sizes, and modification dates. Handles Ollama-not-running and network-timeout errors.
- **Fallback mechanism** — `OllamaProvider` stores `primaryURL`, `fallbackURL` (`localhost:11434`), and `activeURL`. The health monitor pings `primaryURL` via `GET /api/tags` every 30s (configurable); after 3 consecutive failures it switches `activeURL` to fallback and logs a warning; on recovery it switches back and logs info. All `Process`/`ProcessStream` calls use `activeURL`. The monitor starts when a remote URL is configured, stops when cleared, and shuts down gracefully via context cancellation. When only local is configured there is no fallback — just the single local endpoint.
- **Endpoint observability** — response metadata reports which endpoint served the request, e.g. `[Endpoint: http://localhost:11434]` or `[Endpoint: local(fallback)]`, alongside existing model/confidence info; switches are logged.
- Existing local-only behavior is unchanged when no remote URL is configured.

## Notable decisions / constraints
- The Ollama HTTP API contract, SmartRouter, agentic loop, and IDE extension code are all unchanged.
- Out of scope: multiple simultaneous endpoints / load balancing, service discovery, non-Ollama backends (tiiny.ai, ONNX Runtime — see AI Engine Agnosticism track), remote GPU/hardware capability detection beyond Ollama's API, and authentication for remote Ollama (Ollama has no native auth).
- The full live failover/recovery cycle was validated via unit tests (mock HTTP server simulating failure/recovery) plus end-to-end plumbing confirmed against a real Mac Studio remote.
