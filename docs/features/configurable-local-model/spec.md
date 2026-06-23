# Configurable Local Model

## Overview

Cercano's model configuration was inconsistent: the local model (`qwen3-coder`) was hardcoded at server startup via an env var and required a restart to change, while cloud provider config was sent per-request on every gRPC call. This feature unified both behind a single `UpdateConfig` RPC, making the local model *and* the cloud provider runtime-configurable with no server restart.

## Design / Architecture

A new `UpdateConfig(UpdateConfigRequest) returns (UpdateConfigResponse)` RPC was added to the Agent service:

```protobuf
message UpdateConfigRequest {
  string local_model    = 1;
  string cloud_provider = 2;
  string cloud_model    = 3;
  string cloud_api_key  = 4;
}
message UpdateConfigResponse {
  bool   success = 1;
  string message = 2;
}
```

The per-request `CloudProviderConfig provider_config` field (and the message itself) was removed from `ProcessRequestRequest` — a clean break, since the project is pre-release. The `ProviderConfig` struct and the per-request `CloudFactory` routing branch were also removed; cloud provider now always comes from `ModelProviders["CloudModel"]`.

Data flow:

```
VS Code setting change → extension config watcher
  → client.updateConfig(localModel, cloudProvider, cloudModel, cloudApiKey)
  → gRPC UpdateConfig
  → server handler:
      localProvider.SetModelName(...)
      cloudFactory(provider, model, key) → newProvider
      smartRouter.SetCloudProvider(newProvider)
      coordinator.SetCloudProvider(newProvider)
  → next request uses updated config automatically
```

Key components:
- **`OllamaProvider`** (`llm/ollama.go`) — gained `SetModelName(name)` and a `sync.RWMutex`; `Name()` and `Process()` read the model name under a read lock.
- **`SmartRouter`** (`agent/router.go`) — gained `SetCloudProvider(p)` updating `ModelProviders["CloudModel"]` under a write lock, plus an `RWMutex` guarding the map.
- **`ADKCoordinator`** (`loop/adk_coordinator.go`) — gained `SetCloudProvider(p)` updating its `cloudProvider` field.
- **`Server`** (`server/server.go`) — now holds references to `localProvider`, `router`, `coordinator`, and a `cloudFactory` closure; `UpdateConfig` applies local and/or cloud changes and logs them.
- **Extension** (`client.ts`, `extension.ts`) — added an `updateConfig` client method and a `sendConfig()` helper that reads current settings + secrets; removed the per-request `providerConfig` parameter from `processStream()`/`process()`.

## Key Behaviors / Capabilities

- Local model changes at runtime (e.g. to `GLM-4.7-Flash`) with no server restart.
- Cloud provider/model/key changes at runtime; picked up on the next request.
- The extension calls `UpdateConfig` on activation (initial config from settings/secrets) and again whenever a relevant setting changes — only a `cercano.server.port` change still triggers a server restart (needs a new listener).
- A single shared `OllamaProvider` instance is used by both the `SmartRouter` and `ADKCoordinator`, so a model-name update is visible to both via Go pointer semantics.

## Notable Decisions / Constraints

- **Thread safety** — `sync.RWMutex` on `OllamaProvider` (and on the router's provider map) guards concurrent request vs. config access.
- **Cloud provider replacement** must update references in both the `SmartRouter` and the `ADKCoordinator`; the coordinator builds a new generator agent per request, so the new reference is picked up next request.
- **`CERCANO_LOCAL_MODEL` env var retained** as the startup default; `UpdateConfig` overrides at runtime.
- **No proto field renumbering** — removing the per-request field leaves a numbered gap rather than reassigning numbers, preserving wire compatibility (`conversation_id` retained as its own field).
