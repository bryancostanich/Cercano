# `cercano_dispatch` — Raw Local-LLM Tool-Use Dispatch

> Status: **Superseded** by `docs/features/cli/native-tool-calling/design.md` for V1. The native tool calling design covers both Anthropic and Ollama uniformly through a layered provider abstraction and includes the confirm-gating UI dispatch.md deferred. The host-LLM cancellation, conversation_id continuity, and MCP progress-event goals can move to a follow-up if still wanted.

## Overview / Goal

### Problem

`cercano_local` is the only path for a host LLM to drive Cercano's local model directly, and it bundles too much: SmartRouter classification, optional cloud escalation, and (with `file_path` + `work_dir`) an agentic generate-validate retry loop. There is no clean way for a host LLM to say "here is a task, run the local model as an autonomous agent with full tool-use capability, stream me what happens, let me interrupt, and don't second-guess my prompting."

### Goals

1. Host LLM can dispatch a task to the local LLM and get a multi-turn agentic tool-use loop, without Cercano's router / validator / escalation logic getting in the way.
2. Local LLM has built-in tools for `read_file`, `write_file`, `shell_exec`, `web_fetch`.
3. Host sees events as they happen (tool calls, tool results, model text) via MCP progress notifications.
4. Host can cancel the loop mid-execution.
5. Conversation state is kept server-side (same model as `cercano_local`) and survives across calls via `conversation_id`.
6. Zero cloud tokens are spent inside the loop — strictly local, regardless of what the local model says.

### Non-Goals

- Sandboxing. Tools run with the full permissions of the cercano process; the host is trusted to bound the task.
- Cloud escalation. If the host wants cloud, the host uses its own cloud model.
- Token-level streaming from Ollama in v1 (events fire per assistant turn, not per token).
- Parallel tool execution. Tool calls in a single assistant turn run sequentially.
- Per-tool permission allowlists from the host. v1: all four built-ins are always available.
- Custom / user-defined tools. The registry is built-in only in v1.
- Pluggable engines beyond Ollama in v1.

## Design / Approach

### Architecture

```
┌──────────────┐         MCP tool call            ┌─────────────────────┐
│ Cloud Agent  │ ───────────────────────────────► │  Cercano MCP        │
│ (Claude Code)│                                  │  handleDispatch()   │
│              │ ◄── progress notifications ───── │                     │
│              │     {tool_call, tool_result,     └──────────┬──────────┘
│              │      text_chunk, done}                      │
└──────────────┘ ─── notifications/cancelled ───►            ▼
                                                  ┌─────────────────────┐
                                                  │  dispatch.Loop      │
                                                  │  engine.ChatWithTools (Ollama)
                                                  │  tool registry:     │
                                                  │   read_file,        │
                                                  │   write_file,       │
                                                  │   shell_exec,       │
                                                  │   web_fetch         │
                                                  └─────────────────────┘
```

- **No SmartRouter, no validator, no cloud escalation.** The dispatch path bypasses everything that makes `cercano_local` "smart."
- **Loop runs server-side.** Host only sees the prompt going in and events streaming out — not individual Ollama round-trips.
- **Built on Ollama's native tool-use protocol** (`/api/chat` with the `tools` parameter). Requires a tool-use-capable model (`qwen3-coder`, `llama3`, etc.); error out clearly when the active model lacks support.
- **Streaming via MCP progress notifications** (`notifications/progress`) using a `progressToken` allocated at call start. Final assistant text + summary returned as the MCP tool result.
- **Cancellation via MCP `notifications/cancelled`** — closes the in-flight Ollama HTTP request and exits the loop cleanly.
- **Conversation store reused** from `cercano_local`, schema extended to carry assistant-with-tool-calls and tool-result turns (transparent to the existing chat path).

### Components

```
source/server/internal/
├── dispatch/
│   ├── dispatch.go     # Loop runner       + _test.go
│   ├── events.go       # Event sum-type    + _test.go
│   ├── tools.go        # Tool interface + Registry + _test.go
│   ├── store.go        # History + Store   + _test.go
│   └── builtin/
│       ├── read_file.go    + _test.go
│       ├── write_file.go   + _test.go
│       ├── shell_exec.go   + _test.go
│       └── web_fetch.go    + _test.go  (reuses internal/web)
├── engine/engine.go        # extend InferenceEngine with ChatWithTools
├── engine/ollama/ollama.go # implement ChatWithTools against /api/chat
└── mcp/server.go           # New DispatchRequest schema + handleDispatch
```

### Boundary contracts

**`dispatch.Tool`** — `Name() string`, `Schema() ToolSchema`, `Run(ctx, args json.RawMessage) (result string, err error)`. The `result` string is fed back to the model as the tool-result message. `err != nil` means "tool failed to execute"; the loop converts it to `ToolResult{ok: false, content: err.Error()}` and feeds that back.

**`dispatch.Registry`** — `Register(t Tool) error` (dup name → error), `Get(name) (Tool, bool)`, `Schemas() []ToolSchema`.

**`dispatch.Event`** (tagged struct) — `EventKind ∈ {EventTextChunk, EventToolCall, EventToolResult, EventDone}`. Fields: `Text` (TextChunk); `ToolCallID`/`ToolName`/`ToolArgs` (ToolCall); `ToolResult`/`ToolOK` (ToolResult); `DoneError`/`Cancelled` (Done). Marshals to JSON with `kind` rendered as its string form.

**`dispatch.Loop`** — `NewLoop(engine, registry, model, maxTurns)`; `Run(ctx, seed History, userMsg) (<-chan Event, finalHistory func() []ChatMessage)`. Starts a goroutine, returns the event channel and a `finalHistory` accessor valid after the channel closes. Default `maxTurns = 50`.

**`engine.InferenceEngine` (extended)** — add `ChatWithTools(ctx, req ChatRequest) (ChatResponse, error)`. `ChatRequest` carries `Model`, `Messages` (with new tool-message shapes), and `Tools` (`[]ToolSchemaJSON`). `ChatResponse` carries `Content string`, `ToolCalls []ToolCall`, token counts. Existing `Chat`/`Generate`/`Complete` methods untouched.

### MCP surface

New tool `cercano_dispatch`. `DispatchRequest{ Prompt, System (optional override), ConversationID (optional), cloudTokenFields }`. Returns:
```json
{ "text": "<final assistant text>",
  "summary": { "turns": 3, "tool_calls_made": 5, "cancelled": false } }
```
Tool description: "Dispatch a task to Cercano's local LLM as an autonomous agent with full tool-use capability (read_file, write_file, shell_exec, web_fetch). Runs an agentic loop locally ... Streams events back as progress notifications so you can see what's happening; cancel any time. No cloud calls, no validator loop, no SmartRouter — raw local dispatch under your control. Multi-turn via conversation_id."

### Data flow (per call)

1. Host calls `cercano_dispatch`. MCP handler allocates a `progressToken`, loads history for `conversation_id` (empty if new), appends user message with `prompt`, calls `dispatch.Loop.Run`.
2. Per turn: build `ChatRequest{Model, Messages, Tools}`, call `engine.ChatWithTools`. If response has `ToolCalls`: for each (sequential) emit `EventToolCall`, look up tool, parse args, run, emit `EventToolResult`, append assistant-with-tool-call + tool-result messages; continue. Else emit `EventTextChunk` + `EventDone{cancelled:false}` and return. On `ctx.Done()` emit `EventDone{cancelled:true}`. At turn >= maxTurns emit `EventDone{error:"exceeded max turns (50)"}`.
3. MCP handler drains channel, sends one `notifications/progress` per event (event JSON), persists updated `History`, returns final assistant text + summary.

### Conversation history schema extension

Existing storage holds `[{role, content}]`. Extend to support `assistant` messages with `tool_calls: [{id, name, arguments}]` and `tool` messages with `tool_call_id` + `content`. The store keeps records as opaque blobs keyed by `conversation_id`; only the dispatch loop interprets the new fields. `cercano_local` continues writing the old 2-field shape; mixing `cercano_local` and `cercano_dispatch` on the same `conversation_id` is undefined behavior (documented). The dispatch `Store` uses a separate session-ID keyspace (`dispatch-` prefix) from `agent.ConversationStore`.

### Tool-by-tool behavior

| Tool | Args | Result | Error to model |
|---|---|---|---|
| `read_file` | `{path}` | utf-8 file contents | not found, permission, binary (NUL in first 8 KB) |
| `write_file` | `{path, content, create_dirs?}` | `"wrote N bytes to <path>"` | permission, parent missing (when `create_dirs=false`) |
| `shell_exec` | `{command, cwd?, timeout_sec? (60)}` | `"exit_code: N\nstdout: ...\nstderr: ..."` | exec/spawn failure, timeout. Non-zero exit is data, not error. |
| `web_fetch` | `{url}` | extracted plain-text (reuses `internal/web`) | network, non-2xx, parse |

### Error handling

| Source | Behavior |
|---|---|
| Tool returns error | `ToolResult{ok:false}`, fed back, loop continues |
| Unknown tool requested | `ToolResult{ok:false, "tool '<name>' not registered"}`, continues |
| Invalid tool args | `ToolResult{ok:false, "invalid arguments: ..."}`, continues |
| Engine error (Ollama down) | `Done{error}`, MCP returns error result |
| Model lacks tool-use | `Done{error: "model '<name>' does not support tool use; switch to qwen3-coder ..."}` |
| Host cancellation | `Done{cancelled:true}`, partial history persisted |
| maxTurns hit | `Done{error:"exceeded max turns (50)"}`, partial history persisted |

### Implementation notes

- **Ollama tool-use payload.** `/api/chat` accepts `tools: [{type:"function", function:{name,description,parameters}}]` and returns assistant messages with optional `tool_calls`. Older Ollama may omit tool-call IDs — generate synthetic `tc_<idx>` before storing.
- **Model capability check.** Cheap probe before first chat; cache per `(endpoint, model)`. Fail fast with clear error if tools unsupported.
- **Cancellation plumbing.** MCP `notifications/cancelled` handler calls `cancel()` on a `context.WithCancel(parent)`. `ChatWithTools` honors `ctx` via `http.NewRequestWithContext`.
- **No mutex hell.** `Loop.Run` single-goroutine internally; event channel is the only sync point; `History` returned only after channel closes.
- **`shell_exec` safety.** No sandbox, runs as cercano process user — documented in README and tool description.

### Out of scope (future work)

Per-tool host allowlists; custom/user-registered tools; token-level streaming; parallel tool execution within a turn; cloud-engine `ChatWithTools` (Gemini/Anthropic); sandbox/chroot/capability restrictions.

## Plan / Tasks

Architecture: a new `internal/dispatch` package owns the agentic loop; built-in tools live in `internal/dispatch/builtin/`. `engine.InferenceEngine` gains `ChatWithTools` implemented against Ollama's `/api/chat`. A new `dispatch.Store` persists structured history via the existing `session.Service`. The MCP handler issues a progress token, drains events, emits one progress notification per event.

Tech stack: Go 1.25, `net/http` for Ollama, `encoding/json`, `os/exec` for `shell_exec`, existing `internal/web` `Fetcher` for `web_fetch`, existing `session.Service` (Google ADK) for history, existing `notifyProgress` helper.

> Each task follows superpowers TDD/subagent flow: write failing test → run/verify fail → implement → run/verify pass → commit.

### Task 1: `Event` types
- [ ] Step 1: Write failing test `dispatch/events_test.go` (EventKind.String, JSON marshal of kind/tool_name).
- [ ] Step 2: Run test to verify it fails (package missing).
- [ ] Step 3: Implement `dispatch/events.go` (`EventKind` enum, `String()`, `MarshalJSON`, `Event` struct).
- [ ] Step 4: Run test to verify it passes.
- [ ] Step 5: Commit.

### Task 2: `Tool` interface and `Registry`
- [ ] Step 1: Write failing test `dispatch/tools_test.go` (register/get, duplicate-name error, missing→false, Schemas).
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Implement `dispatch/tools.go` (`ToolSchema`, `Tool`, `Registry`, `NewRegistry`, `Register`, `Get`, `Schemas`).
- [ ] Step 4: Run test to verify it passes.
- [ ] Step 5: Commit.

### Task 3: `read_file` built-in tool
- [ ] Step 1: Write failing test `builtin/read_file_test.go` (happy path, missing file, binary detection, permission denied, bad args).
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Implement `builtin/read_file.go` (8 KB binary-detect window, NUL check).
- [ ] Step 4: Run test to verify it passes.
- [ ] Step 5: Commit.

### Task 4: `write_file` built-in tool
- [ ] Step 1: Write failing test `builtin/write_file_test.go` (happy, overwrite, create_dirs true, create_dirs false errors, bad args).
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Implement `builtin/write_file.go`.
- [ ] Step 4: Run test to verify it passes.
- [ ] Step 5: Commit.

### Task 5: `shell_exec` built-in tool
- [ ] Step 1: Write failing test `builtin/shell_exec_test.go` (zero exit, non-zero exit is data, stderr captured, timeout, cwd, bad args).
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Implement `builtin/shell_exec.go` (`sh -c`, default 60s timeout, `exec.ExitError` → exit_code data not Go error).
- [ ] Step 4: Run test to verify it passes.
- [ ] Step 5: Commit.

### Task 6: `web_fetch` built-in tool
- [ ] Step 1: Write failing test `builtin/web_fetch_test.go` (HTML extracted, non-200 errors, malformed URL, bad args).
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Implement `builtin/web_fetch.go` (thin wrapper over `web.NewFetcher()`).
- [ ] Step 4: Run test to verify it passes.
- [ ] Step 5: Commit.

### Task 7: Extend `engine.InferenceEngine` with `ChatWithTools`
- [ ] Step 1: Write failing compile-check `engine/chat_types_test.go` (ChatRequest JSON shape).
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Add types to `engine/engine.go` (`ChatMessage`, `ToolCall`, `ToolCallFunc`, `ToolSchemaJSON`, `ToolFunctionJSON`, `ChatRequest`, `ChatResponse`) and add `ChatWithTools` to the interface.
- [ ] Step 4: Run test to verify it passes (project build will fail until Task 8 — expected).
- [ ] Step 5: Commit.

### Task 8: Implement `ChatWithTools` in `OllamaEngine`
- [ ] Step 1: Write failing test `engine/ollama/ollama_chat_test.go` (plain text, tool-call response w/ synthetic ID, HTTP error, context canceled).
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Implement `ChatWithTools` against `/api/chat` (stream:false, num_ctx 32768, synthetic IDs when omitted, token counts).
- [ ] Step 4: Run test + `go build ./...` to verify pass.
- [ ] Step 5: Commit.

### Task 9: `dispatch.Store` for structured history
- [ ] Step 1: Write failing test `dispatch/store_test.go` (append/load, empty ID returns empty, round-trips tool turns).
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Implement `dispatch/store.go` (`NewStore(svc, maxItems)`, `Save`, `Load`, `getOrCreate`; `dispatch-` session-ID prefix; max-items trimming).
- [ ] Step 4: Run test to verify it passes.
- [ ] Step 5: Commit.

### Task 10: `dispatch.Loop` orchestrator
- [ ] Step 1: Write failing test `dispatch/dispatch_test.go` (scripted engine + echo/erroring tools): plain text, single tool call then text, tool error fed back continues, unknown tool fed back, invalid args fed back, cancellation, max-turns cap, history accumulates.
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Implement `dispatch/dispatch.go` (`NewLoop`, `Run` goroutine, `runTool`, `schemasAsJSON`).
- [ ] Step 4: Run test to verify it passes.
- [ ] Step 5: Commit.

### Task 11: Register `cercano_dispatch` MCP tool
- [ ] Step 1: Write failing handler test in `mcp/server_test.go` (text-only response → result contains text + `turns:1`; streams events as progress: tool_call, tool_result, text_chunk, done). Implement helpers modeled on existing `server_test.go` fakes.
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Add `DispatchRequest` schema, `handleDispatch`, `cercano_dispatch` registration, `Server` fields (`dispatchLoop`, `dispatchStore`, `dispatchModelFor`), `WithDispatch` option, and `builtinRegistry()` helper to `mcp/server.go`.
- [ ] Step 4: Run handler test + `go build ./...` + full `go test ./...` to verify pass.
- [ ] Step 5: Commit.

### Task 12: Wire dispatch into both binaries
- [ ] Step 1: Read current wiring in `cmd/cercano/main.go` and `cmd/agent/main.go`.
- [ ] Step 2: Add dispatch wiring after `sessionSvc` (registry with 4 built-ins, `NewLoop`, `NewStore`, `modelResolver`); append `mcp.WithDispatch(...)` to `mcp.NewServer(...)`.
- [ ] Step 3: Apply the same change to `cmd/agent/main.go`.
- [ ] Step 4: `go build ./...` + `go test ./...`.
- [ ] Step 5: Commit.

### Task 13: End-to-end smoke verification (manual)
- [ ] Step 1: Build the binary (`make build`).
- [ ] Step 2: Confirm `cercano_dispatch` is registered (gRPC reflection / MCP probe).
- [ ] Step 3: Drive a two-turn read_file task against real Ollama + qwen3-coder; assert progress events in order (tool_call → tool_result → text_chunk → done) and final result.
- [ ] Step 4: Verify cancellation (sleep 30 prompt, cancel after 2s → done{cancelled:true} within ~1s, no lingering child).
- [ ] Step 5: Verify multi-turn continuation across two calls with same `conversation_id`.
- [ ] Step 6: Commit any README docs touch-ups (add `cercano_dispatch` to tools table).

## Open Questions / Notes

None at design time. Tool description wording, model-capability error text, and the exact progress-notification payload shape can be polished during implementation.
