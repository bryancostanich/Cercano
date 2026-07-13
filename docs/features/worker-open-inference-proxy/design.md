# Worker open-inference proxy

## Problem

In worker execution mode (`execution_mode: worker`, the default), the worker
process builds its own LLM providers from a config snapshot. The **open**
provider is hardcoded to an Ollama client whenever `OllamaURL` is set
(`buildWorkerProviders`, worker.go), with no regard for `open_runtime`. The
worker has no runtime manager (a main-process singleton that owns the
llama-server subprocesses via a cross-process spawn lock), and its config
snapshot doesn't even carry `LlamaServer` settings (wire.go leaves them zero).

Result: with `open_runtime: llama_server`, the worker still calls Ollama at
`localhost:11434`. Co-processor dispatches run inside the worker, so they hit a
dead Ollama endpoint instead of llama-server.

## Approach (chosen: proxy open inference to the host)

The host process owns the llama-server runtime manager and already builds the
correct open provider (`openProviderFor` → llama engine). Mirror the existing
worker→host proxies (credentials, permissions, persistence): the worker's open
provider becomes a **stream proxy** that forwards `Chat`/`StreamChat` to the
host over the existing bidi `RunTurn` stream; the host runs the call through its
real open provider and streams events back.

This keeps a single owner of the llama-server lifecycle (the host) and makes
worker mode fully support llama-server for both main and background work.

## Wire protocol (proto additions)

`WorkerToHost.oneof`:
- `OpenInferenceRequest open_request = 8`

`HostToWorker.oneof`:
- `OpenInferenceEvent open_event = 5`

Messages:
- `OpenInferenceRequest { uint64 id; LLMChatRequest request }` — always
  streaming; the worker's non-streaming `Chat` accumulates events locally into a
  `ChatResponse`, so no separate response message is needed.
- `LLMChatRequest { string model; string system; repeated LLMMessage messages;
  repeated LLMTool tools; string tool_choice; int32 max_tokens; double
  temperature }` — reuses `LLMMessage` (MarshalMessage) for history.
- `LLMTool { string name; string description; bytes input_schema }`.
- `OpenInferenceEvent { uint64 id; oneof kind { LLMStreamEvent event; string
  error; bool done } }`.
- `LLMStreamEvent` — field-for-field mirror of `llm.StreamEvent`
  (type, text_delta, tool_use_id, tool_name, tool_input_raw, reasoning_id,
  reasoning_data, stop_reason, input_tokens, output_tokens, err_text).

## Components

1. **Worker side — `streamOpenProvider`** implements `llm.Provider`:
   - `StreamChat`: assign a monotonic id, register a pending channel, send
     `OpenInferenceRequest`, return a `StreamReader` that yields events routed
     back by id until `done`/`error`. Mirrors `streamCredentialSource`.
   - `Chat`: run `StreamChat` and accumulate into a `ChatResponse`.
   - `Name`/`Capabilities`: static (llama-server capabilities), or fetched once.
   - Replaces the Ollama client as the worker's `openProv` when the configured
     open runtime is not Ollama.

2. **Host side — inference handler** in the workerRunner message loop
   (host.go): on `OpenInferenceRequest`, call the host's open provider's
   `StreamChat` in a goroutine (off the drain path, like `CredRequest`), relay
   each event as `OpenInferenceEvent`, terminate with `done` or `error`.
   Honors ctx cancel (worker `Cancel`).

3. **Wiring**: give the workerRunner a handle to the host's open provider — a
   factory (`func() llm.Provider`) so a runtime/config swap is honored per
   request, mirroring how the server resolves providers fresh. Route the
   worker's `openProv` to `streamOpenProvider` when `open_runtime != "ollama"`.

## Phases

0. Proto messages + regenerate. (this doc)
1. Marshaling helpers: `ChatRequest`/`Tool`/`StreamEvent` ↔ proto.
2. Worker `streamOpenProvider` + routing in `buildWorkerProviders`.
3. Host inference handler + open-provider factory wiring.
4. Tests: id routing, stream relay, cancel, Chat accumulation, Ollama-vs-proxy
   selection by open_runtime.

## Non-goals

- Embeddings proxy (separate path; this covers chat/stream inference).
- Changing in-process mode (already correct).
