# Track Plan: Configurable Local Model

The local model (`qwen3-coder`) is hardcoded at server startup via env var. Cloud provider config is sent per-request on every gRPC call. Both patterns are inconsistent and the local model requires a server restart to change. This track unifies both under a single `UpdateConfig` RPC for runtime configuration.

## Phase 1: `UpdateConfig` RPC — Runtime Model & Provider Configuration

### Objective
Replace the per-request `CloudProviderConfig` pattern and startup-only local model env var with a single `UpdateConfig` RPC. Both local model and cloud provider become runtime-configurable without server restart.

### Key Design Decisions
- **Single `OllamaProvider` instance** shared by SmartRouter and ADKCoordinator — updating `ModelName` is visible to both (Go pointer semantics)
- **Cloud provider replacement** requires updating references in both SmartRouter (`ModelProviders["CloudModel"]`) and ADKCoordinator (`cloudProvider`). The coordinator creates a new generator agent per-request, so updated references are picked up on the next request
- **Thread safety** via `sync.RWMutex` on OllamaProvider for concurrent request/config access
- **`CERCANO_LOCAL_MODEL` env var retained** as startup default — `UpdateConfig` overrides at runtime
- **`CloudProviderConfig` removed from `ProcessRequestRequest`** — clean break (pre-release project)
- **Extension calls `UpdateConfig` on activation** (sends initial config from settings/secrets) and again on setting changes — no server restart needed

### Data Flow

```
VS Code Setting Change → extension.ts config watcher
  → client.updateConfig(localModel, cloudProvider, cloudModel, cloudApiKey)
  → gRPC UpdateConfig RPC
  → Server handler:
      → localProvider.SetModelName("GLM-4.7-Flash")
      → cloudFactory("anthropic", "claude-3-opus", key) → new provider
      → smartRouter.SetCloudProvider(newProvider)
      → coordinator.SetCloudProvider(newProvider)
  → Next request uses updated config automatically
```

### Tasks

#### Task 1: Proto Changes + Code Generation
- Add `rpc UpdateConfig(UpdateConfigRequest) returns (UpdateConfigResponse)` to the Agent service
- Add messages:
  ```protobuf
  message UpdateConfigRequest {
    string local_model = 1;
    string cloud_provider = 2;
    string cloud_model = 3;
    string cloud_api_key = 4;
  }
  message UpdateConfigResponse {
    bool success = 1;
    string message = 2;
  }
  ```
- Remove `CloudProviderConfig provider_config = 2` from `ProcessRequestRequest`
- Remove `CloudProviderConfig` message definition
- Regenerate Go stubs: `protoc --go_out=... --go-grpc_out=... agent.proto`
- Regenerate JS/TS stubs: `cd source/clients/vscode && npm run gen:proto`
- Add `conversation_id` back as field 5 (was after provider_config) — renumber fields: `input=1, work_dir=2, file_name=3, conversation_id=4`
- Verify: `go build ./...` passes

**Note on proto field numbering:** We are NOT renumbering — proto fields are identified by number, not position. We simply remove field 2 and leave a gap. This is standard protobuf practice and maintains wire compatibility.

#### Task 2: Server-Side Config Methods (TDD)

**Tests first** (in `ollama_test.go` and `router_test.go`):
- `TestOllamaProvider_SetModelName` — verify model name update is reflected in `Name()` and used in subsequent `Process()` calls
- `TestSmartRouter_SetCloudProvider` — verify cloud provider update in `ModelProviders` map
- `TestSmartRouter_SelectProvider_UsesStoredCloud` — verify routing uses stored cloud provider (no per-request config)

**Implementation:**
- `OllamaProvider` (`llm/ollama.go`): Add `sync.RWMutex`, `SetModelName(name string)` method, wrap `Name()` and `Process()` model access with read lock
- `SmartRouter` (`agent/router.go`): Add `SetCloudProvider(p ModelProvider)` — updates `ModelProviders["CloudModel"]` under write lock. Add `sync.RWMutex` for `ModelProviders` access
- `ADKCoordinator` (`loop/adk_coordinator.go`): Add `SetCloudProvider(p ModelProvider)` — updates `cloudProvider` field
- Remove `ProviderConfig *ProviderConfig` from `agent.Request` struct
- Remove `ProviderConfig` struct
- Update `SelectProvider` to remove the per-request `CloudFactory` branch — cloud provider always comes from `ModelProviders["CloudModel"]`
- Update all tests that reference `ProviderConfig`

#### Task 3: `UpdateConfig` gRPC Handler

- Update `Server` struct in `server/server.go`:
  ```go
  type Server struct {
      proto.UnimplementedAgentServer
      agent         *agent.Agent
      localProvider *llm.OllamaProvider
      router        *agent.SmartRouter
      coordinator   *loop.ADKCoordinator
      cloudFactory  agent.CloudFactory
  }
  ```
- Update `NewServer()` to accept additional params
- Implement `UpdateConfig(ctx, req) -> (resp, error)`:
  - If `local_model` is non-empty: call `localProvider.SetModelName(localModel)`, log the change
  - If any cloud fields are set: call `cloudFactory(ctx, provider, model, apiKey)`, then `router.SetCloudProvider(newProvider)` + `coordinator.SetCloudProvider(newProvider)`, log the change
  - Return `{success: true, message: "..."}`
- Remove `ProviderConfig` mapping from `mapRequest()`
- Update `main.go` to pass `localProvider`, `smartRouter`, `coordinator`, and the cloud factory closure to `NewServer()`

#### Task 4: Extension Client + UI Changes

- `client.ts`:
  - Add `updateConfig(config: {localModel?, cloudProvider?, cloudModel?, cloudApiKey?})` method — calls the new `UpdateConfig` RPC
  - Remove `providerConfig` parameter from `processStream()` and `process()` signatures
  - Remove `CloudProviderConfig` usage from request building
- `extension.ts`:
  - Add a `sendConfig()` helper that reads current settings + secrets and calls `client.updateConfig()`
  - Call `sendConfig()` after client initialization on activation (sends initial config)
  - Config change watcher: call `sendConfig()` instead of restarting server for model/provider changes. Keep server restart only for `cercano.server.port` changes (requires new listener)
  - Remove `providerConfig` from `processStream()` calls in the chat participant
  - Simplify provider resolution block (no longer needed per-request)

#### Task 5: Conductor - User Manual Verification

Verify:
1. `go test ./... -count=1` — all tests pass
2. `cd source/clients/vscode && npm run compile` — extension builds
3. Manual: F5 to launch extension
   - Chat with Cercano — works with default local model
   - Change local model via `@cercano /config` → Set Local Model → `GLM-4.7-Flash`
   - Chat again — should use `GLM-4.7-Flash` (no server restart, check server output for model name)
   - Set cloud API key, change provider to Google
   - Ask a "use cloud"-style question — should use the configured cloud provider

## Phase 2: CLI Flags (Optional)

### Objective
Add CLI flags to the server binary for standalone usage without env vars.

### Tasks
- [ ] Task: Add `--port`, `--ollama-url`, `--local-model` flags to `main.go` (env vars remain as fallback).
- [ ] Task: Update Server README with CLI usage examples.
- [ ] Task: Conductor - User Manual Verification
