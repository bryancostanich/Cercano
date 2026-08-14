# Embedded Inference Runtime

## Overview

Cercano currently talks to local models through external endpoints, primarily
Ollama. That remains useful, but Cercano should also be able to own a lightweight
local inference runtime itself: download or discover models, start a runtime when
needed, observe it, recover it after failures, and expose its state to clients.

The first embedded runtime should be `llama-server` from `llama.cpp`, run as a
supervised child process. "Embedded" means Cercano manages the runtime binary,
model inventory, process lifecycle, logs, health, and endpoint selection. It does
not mean linking the inference engine into the Cercano process for v1.

Primary goals:

- Keep the inference engine swappable. The Cercano server and CLI should depend
  on a runtime abstraction, not on `llama.cpp` details.
- Start with `llama-server` because it is mature, lightweight, GGUF-native,
  OpenAI-compatible, observable, and broadly supported.
- Add a `cmd+m` CLI dashboard that pulls model, runtime, process, and log data
  from the Cercano server over gRPC.
- Run `llama-server` in process isolation so a model crash, runtime panic, hang,
  or OOM does not normally take down the Cercano server.

## Product Shape

The user-facing experience should feel like this:

1. Existing external Ollama and remote endpoint flows keep working.
2. Users can opt into a managed runtime with config such as
   `local_runtime: llama_server`.
3. Cercano discovers local GGUF files from configured model directories and,
   later, a curated download catalog.
4. When a managed model is selected, the Cercano server starts `llama-server` on
   an internal localhost port, waits for readiness, and routes local inference to
   that endpoint.
5. The CLI dashboard shows downloaded models, available models, running models,
   external LLM endpoints, health, PID, port, restarts, memory hints where
   available, and logs.
6. If `llama-server` fails, Cercano marks the runtime unhealthy, keeps the gRPC
   server alive, restarts the child process according to policy, and lets the UI
   show what happened.

## Current Architecture Fit

Cercano already has the right first layer:

- `internal/engine` defines `InferenceEngine`, `EmbeddingService`, and
  `EngineRegistry`.
- `internal/engine/ollama` isolates Ollama HTTP details.
- `internal/server` exposes `ListModels`, `GetConfig`, and `UpdateConfig`.
- `internal/cli/agentclient` talks to the server over gRPC.
- `internal/cli/ui` owns Bubble Tea key handling and overlays.

The missing layer is below `InferenceEngine`: runtime ownership. Today an engine
assumes some external process already exists. Embedded inference needs a server
owned runtime manager that can answer: what models exist, what processes are
running, which endpoint is active, what logs are available, and what restart
policy applies?

## Design

Add a provider-neutral runtime layer, separate from the request-level engine
interfaces.

Suggested package:

```text
source/server/internal/localruntime/
```

Core interfaces:

```go
type Manager interface {
    Providers() []ProviderInfo
    Inventory(ctx context.Context) ([]ModelRecord, error)
    Instances(ctx context.Context) ([]InstanceRecord, error)
    Start(ctx context.Context, req StartRequest) (*InstanceRecord, error)
    Stop(ctx context.Context, req StopRequest) error
    Restart(ctx context.Context, req RestartRequest) (*InstanceRecord, error)
    Status(ctx context.Context) (*StatusSnapshot, error)
    Logs(ctx context.Context, req LogRequest) ([]LogEntry, error)
}

type Provider interface {
    Name() string
    Capabilities() RuntimeCapabilities
    Discover(ctx context.Context) ([]ModelRecord, error)
    Start(ctx context.Context, req StartRequest, sink LogSink) (*InstanceRecord, error)
    Stop(ctx context.Context, instanceID string) error
    Probe(ctx context.Context, instanceID string) (*InstanceHealth, error)
}
```

This gives us two separations:

- `InferenceEngine` answers model requests.
- `localruntime.Manager` owns binaries, model files, child processes, logs, and
  dashboard data.

The managed `llama-server` engine adapter can still implement the existing
`InferenceEngine` interface by calling the supervised localhost endpoint. Later,
TensorSharp, `mistral.rs`, ONNX Runtime GenAI, MLX, or in-process `llama.cpp`
can plug into the same runtime layer.

## Runtime Data Model

`ModelRecord` should be richer than the existing protobuf `ModelInfo`:

```text
id              stable Cercano model id
display_name    user-facing name
runtime         llama_server, ollama, mistral_rs, tensorsharp, etc.
source          downloaded, configured_path, remote, catalog
path            local path when applicable
format          gguf, ollama, safetensors, unknown
family          qwen, llama, gemma, mistral, unknown
quantization    Q4_K_M, Q8_0, unknown
size_bytes      model file size
modified_at     filesystem or provider timestamp
download_state  not_downloaded, downloading, downloaded, failed
runtime_state   stopped, starting, running, unhealthy, crashed, failed
supports_chat   bool
supports_embed  bool
supports_tools  bool or unknown
supports_vision bool or unknown
mmproj_path     projector path for multimodal GGUF models when available
active          selected for local inference
```

`InstanceRecord` should describe the running process:

```text
id              runtime instance id
runtime         llama_server
model_id        active model
state           starting, running, unhealthy, crashed, restarting, stopped
pid             child process id
address         localhost bind address
port            selected internal port
endpoint        http://127.0.0.1:<port>
started_at      timestamp
ready_at        timestamp
restart_count   supervisor restart count
last_exit_code  child process exit code
last_error      last health or process error
log_path        server-owned log file
```

`EndpointRecord` should describe external inference surfaces that Cercano does
not own as child processes:

```text
id              stable endpoint id
kind            ollama, vexel, anthropic_proxy, openai_compatible, other
display_name    user-facing name
base_url        configured endpoint URL, redacted where needed
scope           local, lan, remote, cloud
state           unknown, healthy, degraded, unreachable, auth_error
active          used for local inference, cloud fallback, embeddings, etc.
models          discovered model names when the endpoint supports listing
last_checked_at timestamp
latency_ms      latest health probe latency
last_error      latest probe/config error
auth_state      none, configured, missing, invalid, unknown
```

External endpoints and managed runtimes should share the same dashboard surface,
but not the same lifecycle controls. Cercano can start, stop, and restart a
managed `llama-server` child process. For external endpoints, Cercano can probe,
list models where supported, update config, and show errors, but it should not
pretend to control processes it does not own.

## Local vision model findings

Vision-capable GGUF models need both the main model and the projector file. The
curated catalog records this with `supports_vision` and `mmproj_file`; the
runtime must launch those records with `--mmproj <projector.gguf>` and must avoid
re-resolving a catalog match to a path-discovered record that lacks projector
metadata.

Measured and tested candidates on Apple Silicon with the Homebrew llama.cpp
build available during the 2026-08 pass:

| Model | Catalog status | Disk | Measured physical RAM | Finding |
|---|---:|---:|---:|---|
| Qwen2.5-VL 3B Q4_K_M | broken | ~2.5 GB + projector | not useful | Emits `@` garbage / hangs even for text-only prompts with the tested GGUF/build combination. |
| Moondream2 F16 | broken | ~3–4 GB + projector | not retained | Loads and generates text, but OpenAI chat vision requests fail with `Failed to tokenize prompt`; raw completion path hallucinated a trivial test image. |
| Gemma 3 4B Q4_K_M | usable fallback | ~3.1 GB total | ~1.6–1.7 GB | Works with `--mmproj --jinja`; good broad scene understanding, poor dense UI optical character recognition (OCR). |
| Gemma 3 12B Q4_K_M | best local candidate | ~7.6 GB total | ~3.5–3.7 GB | Works with `--mmproj --jinja`; much better than 4B for UI screenshots, still below cloud for exact text/status extraction. |

Policy recommendation from those tests:

- Keep image inspection cloud-first whenever cloud is allowed; dense screenshots
  still need cloud-quality OCR and task understanding.
- Use local Gemma as the fallback for `open_only` / offline operation.
- Prefer Gemma 3 12B over 4B as the local vision default when the extra ~2 GB
  physical footprint is acceptable.
- Retain broken catalog entries only for provenance and to prevent accidental
  selection as profile defaults.

Detailed transcripts and exact prompts are captured in
`efforts/local-model-vision/HANDOFF.md`.

## llama-server v1

Start with a managed sidecar provider for `llama-server`.

Responsibilities:

- Find or install a `llama-server` binary. Homebrew installs use the
  `llama.cpp` formula first; pinned Cercano-managed downloads can replace this
  once the runtime catalog exists.
- Verify the binary version and checksum when Cercano manages the binary.
- Discover GGUF models from configured model directories.
- Pick an internal localhost port for each running instance.
- Start `llama-server` with a model path and controlled arguments.
- Wait for readiness before registering the endpoint as available.
- Expose logs and health through the runtime manager.
- Route local generation through a Cercano engine adapter that talks to the
  managed endpoint.

Suggested config:

```yaml
local_runtime: ollama # ollama | llama_server | auto

llama_server:
  enabled: false
  binary: ""          # optional override; otherwise managed binary
  version: ""         # optional pinned managed version
  model_dirs:
    - ~/.cercano/models
  default_model: ""
  context_size: 8192
  gpu_layers: -1
  threads: 0
  extra_args: []
  restart:
    enabled: true
    max_attempts: 3
    backoff: 2s
```

For the MVP, it is fine to require a configured GGUF file or directory. Download
management can follow once the runtime inventory and dashboard are in place.

## Install and Setup

`llama-server` setup should be part of the normal Cercano install path:

- Homebrew installs declare `llama.cpp` as a dependency so `llama-server` is
  present before first run.
- `cercano setup` prepares the managed runtime whether or not the user has
  already opted into `local_runtime: llama_server`.
- Setup creates the canonical GGUF model directory, starting with
  `~/.cercano/models`.
- If `llama-server` is not found, setup prompts to install `llama.cpp` through
  Homebrew on macOS/Linux. Platforms without an automated install path get a
  clear manual instruction and keep the managed runtime disabled.
- If exactly one GGUF model is found in the configured model directories, setup
  records it as `llama_server.default_model`. If several are found, setup leaves
  selection explicit.
- Setup enables `llama_server.enabled` when the binary is available, but it does
  not change `local_runtime` from `ollama` yet. The supervised runtime can be
  displayed and controlled by the dashboard before it becomes the active
  inference engine.

## Failure Isolation

`llama-server` must run outside the Cercano process.

The supervisor should:

- Start `llama-server` with `exec.Command`, its own process group, and no shared
  stdin.
- Bind only to `127.0.0.1` unless the user explicitly opts into another address.
- Pipe stdout and stderr into a server-owned log sink.
- Keep a bounded in-memory ring buffer plus a rotating file on disk.
- Treat readiness as explicit: the child process is not usable until health or
  model endpoints respond successfully.
- Apply per-request timeouts when the Cercano engine calls the runtime.
- Kill and restart the child on health check failure, process exit, or detected
  hangs.
- Use exponential backoff and a restart ceiling to avoid crash loops.
- Mark the model/runtime as `failed` after repeated crashes until the user
  manually retries or changes config.

Expected failure behavior:

```text
llama-server crash
  -> supervisor observes process exit
  -> runtime state becomes crashed/restarting
  -> Cercano gRPC server stays up
  -> dashboard shows exit code and recent logs
  -> supervisor restarts if policy allows

llama-server hang
  -> request timeout or health probe timeout fires
  -> supervisor marks unhealthy
  -> child process is terminated and restarted

model OOM or bad GGUF
  -> child process exits or readiness fails
  -> restart attempts are capped
  -> model is marked failed with last_error
```

This isolation cannot protect against a system-wide OOM killer selecting the
Cercano server itself, but it does prevent ordinary inference runtime failures
from being Go process panics.

## Server API

Do not make the CLI call `llama-server` directly. The CLI should only talk to
the Cercano server.

Add provider-neutral gRPC APIs alongside the existing Ollama-shaped APIs:

```protobuf
rpc GetRuntimeStatus(GetRuntimeStatusRequest) returns (GetRuntimeStatusResponse);
rpc ListRuntimeModels(ListRuntimeModelsRequest) returns (ListRuntimeModelsResponse);
rpc ListRuntimeEndpoints(ListRuntimeEndpointsRequest) returns (ListRuntimeEndpointsResponse);
rpc StartRuntimeModel(StartRuntimeModelRequest) returns (StartRuntimeModelResponse);
rpc StopRuntimeModel(StopRuntimeModelRequest) returns (StopRuntimeModelResponse);
rpc RestartRuntime(RestartRuntimeRequest) returns (RestartRuntimeResponse);
rpc StreamRuntimeLogs(StreamRuntimeLogsRequest) returns (stream RuntimeLogEntry);
```

Keep `ListModels` for backward compatibility, but stop using it as the dashboard
source of truth. It can later delegate to the runtime manager or become a
compatibility view over `ListRuntimeModels`.

`UpdateConfig` should grow provider-neutral local runtime fields rather than
adding more Ollama-specific settings:

```protobuf
string local_runtime = 7;      // ollama, llama_server, auto
string local_model_id = 8;     // provider-neutral selected model id
```

The server should keep owning model selection, process state, and logs. Clients
render what the server reports.

`GetRuntimeStatus` should include both managed runtime instances and external
endpoint records so clients can render one coherent model dashboard without
knowing which endpoints are process-owned.

## CLI Dashboard

The CLI dashboard should open from `cmd+m` where the terminal can deliver that
key. Because macOS terminal applications often reserve Command key chords, also
provide reliable fallbacks such as `/models`, `/runtime`, or a configurable key
binding.

Dashboard data flow:

```text
CLI key or slash command
  -> agentclient.GetRuntimeStatus / ListRuntimeModels / ListRuntimeEndpoints / StreamRuntimeLogs
  -> Cercano server runtime manager
  -> runtime providers, endpoint probes, and supervisor
```

Suggested dashboard views:

- Models: downloaded, catalog available, active, failed, size, quantization,
  runtime, path, and actions.
- Running: runtime, model, PID, port, health, uptime, restarts, last error.
- Endpoints: external Ollama, Vexel, cloud proxies, OpenAI-compatible URLs, and
  other configured endpoints with health, active role, discovered models, auth
  state, latency, and last error.
- Logs: merged Cercano server log and child runtime logs, filterable by source.
- Config: current `local_runtime`, active model, model directories, binary path,
  external endpoint URLs, and restart policy.

Initial actions:

- Activate model.
- Start model.
- Stop model.
- Restart runtime.
- Probe endpoint.
- Switch endpoint.
- Open logs.
- Retry failed model.

The first implementation can be read-mostly. Mutating actions can land once the
status surface is stable.

## Logging

The dashboard needs server-pulled logs, not CLI-side file scraping.

Add a small log hub in the server:

- Replace or wrap direct `fmt.Printf` server logging with a logger that writes to
  stdout, a rotating file, and an in-memory ring.
- Register child process stdout/stderr streams as separate log sources.
- Expose recent logs via unary fetch and live logs via server-streaming gRPC.
- Include source, level, timestamp, runtime id, model id, and message.

Log sources:

```text
cercano.server
cercano.runtime.supervisor
llama_server.<instance_id>.stdout
llama_server.<instance_id>.stderr
```

## Implementation Plan

### Phase 1: Runtime foundation

- Add `internal/localruntime` interfaces and records.
- Add an in-memory runtime manager with fake provider tests.
- Add protobuf messages and server methods for runtime status, inventory,
  process records, endpoint records, and log entries.
- Add `agentclient` wrappers for the new RPCs.
- Keep existing Ollama behavior unchanged.

### Phase 2: llama-server sidecar

- Add `localruntime/llamaserver` provider.
- Support configured binary path first.
- Discover GGUF files from configured directories.
- Start one supervised child process for the selected model.
- Capture logs, readiness, exit status, restart count, and last error.
- Add Homebrew dependency and `cercano setup` preparation for the managed
  runtime.
- Add a `llama_server` engine adapter that implements `InferenceEngine` by
  calling the supervised endpoint.
- Wire `local_runtime: llama_server` into startup and `UpdateConfig`.
- Expose runtime switching through `/config local-runtime llama_server` and the
  `cercano_config` MCP tool.

### Phase 3: CLI dashboard

- Add key binding for `cmd+m` where supported.
- Add `/models` or `/runtime` fallback.
- Build a Bubble Tea overlay using server-pulled runtime status.
- Show models, running instances, external endpoints, and logs.
- Poll lightweight status periodically while the dashboard is open.
- Use streaming logs only in the log view.

### Phase 4: Model catalog and downloads

- Add a curated model manifest for recommended GGUF files.
- Track download state separately from runtime state.
- Add download, cancel, verify checksum, and delete actions.
- Surface available-but-not-downloaded models in the dashboard.

### Phase 5: Runtime extensibility

- Add a second provider behind the same interfaces, likely `mistral.rs` for a
  robust alternate sidecar or TensorSharp as an experimental runtime lab.
- Move any llama-specific assumptions out of shared records.
- Consider in-process `llama.cpp` only after the sidecar path is stable.

## Testing Plan

- Unit test the runtime manager with fake providers.
- Unit test supervisor transitions: start, readiness timeout, crash, restart,
  capped crash loop, stop, and log capture.
- Unit test gRPC mapping between runtime records and protobuf messages.
- CLI tests for dashboard open/close, fallback slash command, status rendering,
  and log view rendering.
- Opt-in integration test with a real `llama-server` binary and tiny GGUF model.
- Manual failure test: start managed runtime, kill the child PID, confirm Cercano
  stays up, dashboard shows the crash, and restart policy behaves as configured.

## Open Questions

- Should `local_runtime: auto` prefer managed `llama-server` when a selected GGUF
  exists, or should existing Ollama remain the default until the user explicitly
  opts in?
- What is the canonical Cercano model directory on macOS, Linux, and Windows?
- After the Homebrew/system-binary MVP, should Cercano package `llama-server`
  binaries or download pinned releases on demand?
- How much model metadata should come from filename parsing versus sidecar model
  inspection?
- Which external endpoint types should ship first beyond existing Ollama config:
  Vexel, generic OpenAI-compatible, Anthropic-compatible proxy, or all of them
  as passive status records?

## Non-goals For v1

- In-process `llama.cpp` via CGO.
- Replacing Ollama support.
- Full multi-model concurrent serving.
- Public network binding for managed runtimes.
- Automatic model downloads without explicit user action.
- GPU-specific optimization UI beyond simple launch arguments.

## References

- `llama.cpp` and `llama-server`: https://github.com/ggml-org/llama.cpp
- Current engine abstraction: `source/server/internal/engine/`
- Current Ollama engine: `source/server/internal/engine/ollama/`
- Current gRPC server config/model APIs: `source/server/internal/server/server.go`
- Current CLI gRPC client: `source/server/internal/cli/agentclient/client.go`
- Current CLI key map: `source/server/internal/cli/ui/keys.go`
