# Spec: `cercano_dispatch` — Raw Local-LLM Tool-Use Dispatch

**Track:** `dispatch_20260530`
**Status:** Design — pending user review

---

## Problem

`cercano_local` is the only path for a host LLM to drive Cercano's local model directly, and it bundles too much: SmartRouter classification, optional cloud escalation, and (with `file_path` + `work_dir`) an agentic generate-validate retry loop. There is no clean way for a host LLM to say "here is a task, run the local model as an autonomous agent with full tool-use capability, stream me what happens, let me interrupt, and don't second-guess my prompting."

## Goals

1. Host LLM can dispatch a task to the local LLM and get a multi-turn agentic tool-use loop, without Cercano's router / validator / escalation logic getting in the way.
2. Local LLM has built-in tools for `read_file`, `write_file`, `shell_exec`, `web_fetch`.
3. Host sees events as they happen (tool calls, tool results, model text) via MCP progress notifications.
4. Host can cancel the loop mid-execution.
5. Conversation state is kept server-side (same model as `cercano_local`) and survives across calls via `conversation_id`.
6. Zero cloud tokens are spent inside the loop — strictly local, regardless of what the local model says.

## Non-Goals

- Sandboxing. Tools run with the full permissions of the cercano process; the host is trusted to bound the task.
- Cloud escalation. If the host wants cloud, the host uses its own cloud model.
- Token-level streaming from Ollama in v1 (events fire per assistant turn, not per token).
- Parallel tool execution. Tool calls in a single assistant turn run sequentially.
- Per-tool permission allowlists from the host. v1: all four built-ins are always available.
- Custom / user-defined tools. The registry is built-in only in v1.
- Pluggable engines beyond Ollama in v1.

---

## Architecture

```
┌──────────────┐         MCP tool call            ┌─────────────────────┐
│ Cloud Agent  │ ───────────────────────────────► │  Cercano MCP        │
│ (Claude Code,│                                  │  handleDispatch()   │
│  etc.)       │ ◄── progress notifications ───── │                     │
│              │     {tool_call, tool_result,     └──────────┬──────────┘
│              │      text_chunk, done}                      │
└──────────────┘ ─── notifications/cancelled ───►            │
                                                             ▼
                                                  ┌─────────────────────┐
                                                  │  dispatch.Loop      │
                                                  │  ┌───────────────┐  │
                                                  │  │ engine        │  │
                                                  │  │ ChatWithTools │  │
                                                  │  │ (Ollama)      │  │
                                                  │  └──────┬────────┘  │
                                                  │         │           │
                                                  │  ┌──────▼────────┐  │
                                                  │  │ tool registry │  │
                                                  │  │ • read_file   │  │
                                                  │  │ • write_file  │  │
                                                  │  │ • shell_exec  │  │
                                                  │  │ • web_fetch   │  │
                                                  │  └───────────────┘  │
                                                  └─────────────────────┘
```

- **No SmartRouter, no validator, no cloud escalation.** The dispatch path bypasses everything that makes `cercano_local` "smart."
- **Loop runs server-side.** Host only sees the prompt going in and events streaming out — not individual Ollama round-trips.
- **Built on Ollama's native tool-use protocol** (`/api/chat` with the `tools` parameter). Requires a tool-use-capable model (`qwen3-coder`, `llama3`, etc.); error out clearly when the active model lacks support.
- **Streaming via MCP progress notifications** (`notifications/progress`) using a `progressToken` allocated at call start. Final assistant text + summary returned as the MCP tool result.
- **Cancellation via MCP `notifications/cancelled`** — closes the in-flight Ollama HTTP request and exits the loop cleanly.
- **Conversation store reused** from `cercano_local`, schema extended to carry assistant-with-tool-calls and tool-result turns (transparent to the existing chat path).

---

## Components

```
source/server/
├── internal/
│   ├── dispatch/
│   │   ├── dispatch.go              # Loop runner
│   │   ├── dispatch_test.go
│   │   ├── events.go                # Event sum-type
│   │   ├── tools.go                 # Tool interface + Registry
│   │   ├── tools_test.go
│   │   └── builtin/
│   │       ├── read_file.go         + _test.go
│   │       ├── write_file.go        + _test.go
│   │       ├── shell_exec.go        + _test.go
│   │       └── web_fetch.go         + _test.go  (reuses internal/web)
│   ├── engine/
│   │   └── interfaces.go            # extend with ChatWithTools method on InferenceEngine
│   ├── engine/ollama/
│   │   └── ollama.go                # implement ChatWithTools against /api/chat
│   └── mcp/
│       └── server.go                # New DispatchRequest schema + handleDispatch
```

### Boundary contracts

**`dispatch.Tool`**

```go
type Tool interface {
    Name() string             // identifier sent to the model
    Schema() ToolSchema       // JSON-schema-shaped description for the model
    Run(ctx context.Context, args json.RawMessage) (result string, err error)
}
```

A tool's `result` string is what gets fed back to the model as the tool-result message. `err != nil` means "tool failed to execute"; the loop converts it to `ToolResult{ok: false, content: err.Error()}` and feeds that back. The model decides what to do with failures.

**`dispatch.Registry`**

```go
type Registry struct { /* map[string]Tool */ }

func (r *Registry) Register(t Tool) error           // dup name → error
func (r *Registry) Get(name string) (Tool, bool)
func (r *Registry) Schemas() []ToolSchema           // for the engine's `tools` param
```

**`dispatch.Event`** (tagged struct):

```go
type EventKind int
const (
    EventTextChunk EventKind = iota
    EventToolCall
    EventToolResult
    EventDone
)

type Event struct {
    Kind        EventKind
    Text        string             // EventTextChunk
    ToolCallID  string             // EventToolCall, EventToolResult
    ToolName    string             // EventToolCall
    ToolArgs    json.RawMessage    // EventToolCall
    ToolResult  string             // EventToolResult
    ToolOK      bool               // EventToolResult
    DoneError   string             // EventDone (empty if clean exit)
    Cancelled   bool               // EventDone
}
```

**`dispatch.Loop`**

```go
type Loop struct {
    engine   engine.InferenceEngine
    registry *Registry
    maxTurns int   // default 50
}

func (l *Loop) Run(ctx context.Context, hist History, userMsg string) (<-chan Event, History, error)
```

`Run` starts a goroutine, returns the event channel and the final `History` (populated on close). The caller (MCP handler) drains the channel, then reads the updated `History` to persist.

**`engine.InferenceEngine` (extended)**

Add one method:

```go
ChatWithTools(ctx context.Context, req ChatRequest) (ChatResponse, error)
```

`ChatRequest` carries `Model`, `Messages` (with the new tool-message shapes), and `Tools` (the `[]ToolSchema`). `ChatResponse` carries `Content string`, `ToolCalls []ToolCall`. Existing `Chat` / `Generate` methods are untouched.

### MCP surface

New tool `cercano_dispatch`:

```go
type DispatchRequest struct {
    Prompt         string `json:"prompt" jsonschema:"The task / instruction for the local LLM."`
    System         string `json:"system,omitempty" jsonschema:"Optional system message override. Defaults to the dispatch system prompt."`
    ConversationID string `json:"conversation_id,omitempty" jsonschema:"Conversation ID for multi-turn dispatch across calls."`
    cloudTokenFields
}
```

Returns:

```json
{
  "text": "<final assistant text>",
  "summary": {
    "turns": 3,
    "tool_calls_made": 5,
    "cancelled": false
  }
}
```

Tool description (one paragraph in the registration call, mirroring the style of the other `cercano_*` tools):

> Dispatch a task to Cercano's local LLM as an autonomous agent with full tool-use capability (read_file, write_file, shell_exec, web_fetch). Runs an agentic loop locally — the model can read code, run commands, fetch URLs, and edit files until it decides the task is done. Streams events back as progress notifications so you can see what's happening; cancel any time. No cloud calls, no validator loop, no SmartRouter — raw local dispatch under your control. Multi-turn via conversation_id.

---

## Data flow

### Per-call flow

1. Host calls `cercano_dispatch`. MCP handler:
   1. Allocates a `progressToken` and returns it via the MCP `progress` capability so subsequent notifications correlate.
   2. Loads history for `conversation_id` (empty if new).
   3. Appends a user message with `prompt`.
   4. Calls `dispatch.Loop.Run(ctx, history, prompt)` → event channel + history reference.
2. For each turn inside `Loop.Run`:
   1. Build `ChatRequest{Model: cfg.LocalModel, Messages: history, Tools: registry.Schemas()}`.
   2. Call `engine.ChatWithTools`.
   3. If response has `ToolCalls`:
      - For each call (sequential): emit `EventToolCall`, look up tool, parse args, run, emit `EventToolResult`, append both an `assistant-with-tool-call` message and a `tool-result` message to history.
      - Continue to next turn.
   4. Else (plain text content): emit `EventTextChunk` with the content, append assistant message to history, emit `EventDone{cancelled: false}`, return.
   5. If `ctx.Done()` between any of the above: emit `EventDone{cancelled: true}`, return.
   6. If turn count >= `maxTurns` (50): emit `EventDone{error: "exceeded max turns (50)"}`, return.
3. MCP handler drains channel; for each event, sends one `notifications/progress` payload with the saved token (event serialized as JSON).
4. After channel closes, handler persists the (now-updated) `History` under `conversation_id` and returns the MCP tool result with the final assistant text + summary fields.

### Conversation history schema extension

Existing storage holds `[{role, content}]`. Extend to support:

```json
{"role": "assistant", "content": "thinking...", "tool_calls": [{"id": "tc_1", "name": "read_file", "arguments": "{\"path\":\"/x\"}"}]}
{"role": "tool", "tool_call_id": "tc_1", "content": "<file contents>"}
```

The conversation store keeps records as opaque blobs keyed by `conversation_id`. Only the dispatch loop interprets the new fields. `cercano_local` continues to write the old 2-field shape; mixed conversations are not supported in v1 (using the same `conversation_id` across `cercano_local` and `cercano_dispatch` is undefined behavior — document this).

### Tool-by-tool behavior

| Tool | Args | Result | Error to model |
|---|---|---|---|
| `read_file` | `{path: string}` | utf-8 file contents | not found, permission, binary detected (NUL byte in first 8 KB) |
| `write_file` | `{path, content, create_dirs?: bool}` | `"wrote N bytes to <path>"` | permission, parent missing (when `create_dirs=false`) |
| `shell_exec` | `{command, cwd?: string, timeout_sec?: int (default 60)}` | `"exit_code: N\nstdout: ...\nstderr: ..."` | exec/spawn failure, timeout. Non-zero exit code is NOT an error — it is data. |
| `web_fetch` | `{url}` | extracted plain-text (reuses `internal/web` HTML stripper) | network, non-2xx, parse |

### Error handling

| Source | Behavior |
|---|---|
| Tool returns error | `EventToolResult{ok: false, content: err.Error()}`, fed back to model, loop continues |
| Model requests unknown tool | `EventToolResult{ok: false, content: "tool '<name>' not registered"}`, fed back, loop continues |
| Model sends invalid tool args | `EventToolResult{ok: false, content: "invalid arguments: <err>"}`, fed back, loop continues |
| Engine returns error (Ollama down, etc.) | `EventDone{error: err.Error()}`, MCP tool returns error result |
| Active model lacks tool-use support | `EventDone{error: "model '<name>' does not support tool use; switch to qwen3-coder or another tool-capable model"}` |
| Host cancellation | `EventDone{cancelled: true}`, partial history persisted, MCP returns the partial result |
| maxTurns hit | `EventDone{error: "exceeded max turns (50)"}`, partial history persisted |

---

## Testing

### Unit tests (fast, no external services)

- **`dispatch/tools_test.go`** — Registry register / lookup / list / dup-name; schema export shape; mock `Tool` implementation.
- **`dispatch/dispatch_test.go`** — Loop with `scriptedEngine` (mock returning preconfigured responses). Cases:
  - Plain text response → 1 turn, `TextChunk` + `Done{cancelled: false}`.
  - Single tool call → `ToolCall` + `ToolResult` + follow-up text + `Done`. 2 turns.
  - Multiple tool calls in one assistant turn → sequential execution in order, paired events.
  - Tool returns error → `ToolResult{ok: false}` emitted, loop continues, next-turn engine call observed.
  - Unknown tool requested → `ToolResult{ok: false, content includes "not registered"}`, loop continues.
  - Invalid args (JSON parse fails) → `ToolResult{ok: false, content includes "invalid arguments"}`, loop continues.
  - Cancellation mid-loop (`ctx.Cancel()` between turns) → `Done{cancelled: true}`, no further engine calls.
  - 50-turn safety cap → `Done{error: "exceeded max turns (50)"}`.
  - History accumulates tool turns in correct order.

- **Built-in tool tests** (one file each):
  - `read_file_test.go` — happy path with `t.TempDir()`, missing file, binary detection (NUL byte), permission denied (chmod 000).
  - `write_file_test.go` — happy path, `create_dirs: true` auto-creates parents, `create_dirs: false` errors on missing parent, overwrite existing.
  - `shell_exec_test.go` — `exit 0` happy, `exit 1` returns exit_code=1 with no Go error, timeout hits before completion → Go error, missing binary → exec error.
  - `web_fetch_test.go` — `httptest.NewServer` returning HTML → extracted text, non-200 → error, malformed URL → error.

### Integration tests (gated)

- **`dispatch_ollama_test.go`** — gated on `OLLAMA_URL` env var; otherwise skipped. Cases:
  - "Read /tmp/<tempfile> and tell me what it says" with a known content tempfile → asserts `read_file` was called and final response contains the content.
  - "Create file X with content 'foo'" → asserts `write_file` was called and the file exists on disk.
  - Long-running `shell_exec` (e.g. `sleep 10`), cancel after 1s → `Done{cancelled: true}` within 2s.

### MCP handler test

- **`mcp/dispatch_handler_test.go`** — fake MCP transport. Asserts:
  - `progressToken` is allocated and returned.
  - Each dispatch event becomes one `notifications/progress` with the right token.
  - Final result includes `text` + `summary{turns, tool_calls_made, cancelled}`.
  - Inbound `notifications/cancelled` cancels the context passed to the loop; `Done{cancelled: true}` reaches the host.

### Fixtures

All in-test via `t.TempDir()` + `httptest.NewServer`. No checked-in fixtures.

---

## Implementation notes

- **Ollama tool-use payload.** Ollama's `/api/chat` accepts a `tools: [{type: "function", function: {name, description, parameters}}]` array and returns assistant messages with optional `tool_calls: [{id?, function: {name, arguments}}]`. Older Ollama versions may not return tool-call IDs; in that case generate a synthetic ID (`tc_<turn>_<idx>`) before storing in history.
- **Model capability check.** Before the first chat call in a session, do a cheap probe to see whether the model supports tools (e.g., try a minimal chat with `tools` set; if Ollama returns an error mentioning tools/functions, fail fast with the clear error). Cache the result per `(endpoint, model)`.
- **Cancellation plumbing.** The `ctx` passed to `Loop.Run` is what the MCP handler hands in. The handler must wire the MCP `notifications/cancelled` handler to call `cancel()` on a `context.WithCancel(parent)` it owns. `engine.ChatWithTools` must honor `ctx` — for the Ollama HTTP call, use `http.NewRequestWithContext`.
- **No mutex hell.** `Loop.Run` is single-goroutine internally — the event channel is the only synchronization with the consumer. The `History` is returned by the loop only after the channel closes, so no shared-mutable-state concerns.
- **`web_fetch` reuse.** `internal/web` already has fetch + HTML strip used by `cercano_fetch`. The dispatch built-in is a thin wrapper.
- **`shell_exec` safety.** Documented explicitly: no sandbox, runs as the cercano process user. The README and tool description should make this clear so users don't deploy this in untrusted multi-tenant contexts without understanding.

## Open questions

None at design time. Tool description wording, model-capability error text, and the exact progress-notification payload shape can be polished during implementation.

## Out of scope (future work)

- Per-tool host allowlists (`allowed_tools` request field).
- Custom / user-registered tools beyond the built-in four.
- Token-level streaming from Ollama.
- Parallel tool execution within a turn.
- Cloud-engine support (`ChatWithTools` for Gemini/Anthropic) — natural next step but out of v1 to keep the surface small.
- Sandbox / chroot / capability restrictions on tool execution.
