# Native Tool Calling

> Status: Planned — design approved, implementation not started.

## Overview / Goal

### Problem

Cercano's agent layer currently exposes `Provider.Process(req string) -> resp string` — text in, text out. To act on the world, the model has to describe an action in prose and the harness has to *infer* what to do from it. There is no structured channel for the model to say "call `read_file` with `path=main.go`" and have that arrive at the harness as data, not as text to parse.

The CLI already has the user-facing pieces of tool calling: a typed `agenttools.Registry` of R/W/X-tier built-ins, an `/tool` slash command, a confirm-prompt UI, and a folded scrollback rendering for tool calls. What's missing is the agent-side machinery that lets the model itself drive those tools through a structured tool-use loop the way Claude Code, Codex CLI, Continue, and Goose do.

### Goals

1. Cloud (Anthropic, via Meridian) and local (Ollama, for tool-capable models) drive Cercano's built-in tools through native tool-use channels — `tool_use` blocks on Anthropic, `tool_calls` arrays on Ollama. No text-prose parsing.
2. The agent loop runs to completion autonomously inside one user turn — model emits tool calls, agent executes, results feed back, model continues — terminating on plain-text output, a hard loop-iteration cap, user denial, or repeated tool failure.
3. The agent layer owns all decision logic. Permission mode, gating, loop control, persistence, and provider adaptation live in `source/server/internal/`. Clients (CLI, VS Code, Zed, future) render UI events and forward user decisions; they do not decide *whether* to gate or *when* to stop.
4. Three explicit permission modes — `strict`, `permissive` (default), `bypass` — govern when the agent pauses for human confirmation. R-tier tools always run silently. The mode is a session property settable via RPC; every client sees the same gate behavior under the same mode.
5. Streaming surfaces tool-use blocks as folded one-line entries that expand on demand; spinners show during execution; per-tool status replaces the spinner on completion.
6. Conversations persist tool-use and tool-result blocks losslessly. `/resume` reconstructs the exact block sequence the next provider call needs — no translation.
7. The provider layer is genuinely abstracted. Adding a new provider (OpenAI, Gemini, Bedrock, llama.cpp) is a new package implementing a shared interface; nothing else changes.

### Non-Goals

- Models that do not support native tool calling. They are rejected with a clear error. No text-mode JSON, ReAct, or XML-tag fallback is built. If a specific legacy model becomes important later, that triggers a new design — it is not an unspecified future feature of this one.
- Anthropic server-side tools (`web_search`, `code_execution`, `web_fetch`, `bash`, `text_editor`). Only client tools are wired.
- MCP host runtime tools. MCP is a separate track; this design covers built-in tools only. The internal `Tool` shape is generic enough that MCP can plug in later without breaking the loop.
- Per-tool permission allowlists (e.g. "auto-approve `write_file` but always prompt on `run_command`"). The three permission modes are uniform across all tools in their tier.
- Token-level streaming of tool-use input deltas. Anthropic streams JSON deltas as tool-use args build up; the UI shows the folded entry the moment a `tool_use` block starts but does not render partial args.
- Parallel execution of W- or X-tier tools in a single turn. They serialize so the confirm gate sees one at a time.
- Conversation export of tool-call turns to markdown.
- Provider-fallback chat-format tool calling (text-mode JSON parsing).

### Supersession

- `docs/plans/dispatch.md` (raw local-LLM tool-use dispatch via MCP) is **superseded for V1**. Native tool calling provides the same loop for both Anthropic and Ollama and includes the confirm-gating UI dispatch.md deferred. The host-LLM cancellation, conversation_id continuity, and MCP progress-event goals from dispatch.md can move to a follow-up if still wanted.
- `docs/plans/cli.md` mentions "tool selection for ambiguous intent (embedding similarity over tool descriptions, LLM fallback only)" as an algorithmic-over-LLM example. With native tool calling, the model picks tools natively *with parameters*; embedding-based pre-classification has no role. That line is removed from `cli.md`.

## Design / Approach

### Layered provider architecture

Provider code is reorganized under `source/server/internal/llm/` with a shared interface up front and one dedicated package per provider that owns the native wire protocol. This is the Continue / Goose pattern, chosen over a normalizer-library approach (litellm-style) because the goal is *native fidelity per provider* — Anthropic's `cache_control`, future `thinking` blocks, image blocks — not lowest-common-denominator coverage.

```
source/server/internal/llm/
├── provider.go              # Provider interface, Capabilities
├── messages.go              # Internal Message + Block (text / tool_use / tool_result)
├── tools.go                 # Internal Tool (name, description, json schema)
├── stream.go                # Internal stream-event types
├── anthropic/
│   ├── client.go            # github.com/anthropics/anthropic-sdk-go (v1.51+)
│   │                        # option.WithBaseURL → Meridian
│   │                        # option.WithHTTPClient → custom RoundTripper for User-Agent
│   ├── adapter.go           # Internal Block ↔ Anthropic ContentBlock
│   └── stream.go            # SSE → internal stream events
├── ollama/
│   ├── client.go            # github.com/ollama/ollama/api (official)
│   ├── adapter.go           # Internal Block ↔ Ollama chat message
│   └── stream.go            # NDJSON → internal stream events
└── absent_cloud.go          # Unchanged sentinel for no-cloud-configured
```

The internal `Block` type is the lingua franca:

```go
type BlockType string
const (
    BlockText       BlockType = "text"
    BlockToolUse    BlockType = "tool_use"
    BlockToolResult BlockType = "tool_result"
)

type Block struct {
    Type       BlockType
    // text block
    Text       string
    // tool_use block
    ToolUseID  string
    ToolName   string
    ToolInput  json.RawMessage
    // tool_result block
    ToolUseRef string
    Content    string
    IsError    bool
    // provider-specific passthrough so adapters can carry features
    // the internal model doesn't enumerate (cache_control, thinking, ...)
    ProviderExtras map[string]any
}
```

The `Provider` interface:

```go
type Provider interface {
    Name() string
    Capabilities() Capabilities
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
    StreamChat(ctx context.Context, req ChatRequest) (StreamReader, error)
}

type Capabilities struct {
    SupportsTools         bool
    SupportsParallelTools bool
    SupportsCaching       bool
    SupportsVision        bool
    MaxToolsPerCall       int  // 0 = unlimited
}

type ChatRequest struct {
    Model       string
    System      string
    Messages    []Message      // ordered history of role + blocks
    Tools       []Tool         // empty = no tool calling
    ToolChoice  ToolChoice     // auto / any / specific / none
    MaxTokens   int
    Temperature float64
}
```

The agent loop reads `Capabilities()` and adapts — if a provider returns `SupportsTools=false`, the loop refuses tool-using requests rather than silently dropping the tools. If `SupportsParallelTools=false`, the agent serializes even R-tier calls.

The current `Provider.Process(req) -> resp` interface (in `internal/agent/agent.go`) is **deprecated for cloud paths** but stays in place for the SmartRouter local-only direct path that does not need tools. A follow-up migrates that path too; not part of this design.

### Anthropic adapter — SDK with Meridian compatibility

The official `anthropic-sdk-go` is used directly. It accepts `option.WithBaseURL("http://127.0.0.1:3456")` (or wherever Meridian is listening) and `option.WithAPIKey("dummy")` — the same pattern `opencode-with-claude` uses against Meridian. The SDK speaks Anthropic-format HTTP, which is exactly what Meridian receives.

The one Meridian-specific concern is User-Agent. The SDK sets `User-Agent: anthropic-go/<version>` by default. If Meridian filters or rewrites based on UA (the `@rynfar/meridian-plugin-opencode-scrub` plugin documents this kind of behavior), we override via `option.WithHTTPClient(...)`:

```go
transport := &uaRoundTripper{base: http.DefaultTransport, ua: "claude-cli/1.0"}
client := anthropic.NewClient(
    option.WithBaseURL(cfg.BaseURL),
    option.WithAPIKey(cfg.APIKey),
    option.WithHTTPClient(&http.Client{Transport: transport}),
)
```

This keeps Meridian compatibility in our hands without depending on SDK changes. The SDK upside is large — tool_use, tool_result, prompt caching, future block types (extended thinking variants, server tool blocks) all arrive as first-class Go types we don't hand-map.

### Ollama adapter

`github.com/ollama/ollama/api` is the official Go client used by `ollama` CLI itself. It exposes `Chat(req *ChatRequest)` which takes a `Tools` field (array of `Tool{Type, Function: {Name, Description, Parameters}}`). The model emits `tool_calls` on the response when it wants to invoke one. NDJSON streaming via the `Stream` callback.

Important caveat: Ollama tool support varies by model. `qwen3-coder`, `qwen2.5-coder`, `llama3.1+`, `deepseek-r1`, `mistral-nemo`, and `granite3.x` all support it; older models do not. The adapter detects via `ollama.Show(model)` and reports through `Capabilities()`. If the configured local model does not support tools and the agent loop needs them, the agent returns a clear error to the client rather than degrading silently. The user can switch models or run without tools.

### Tool catalog assembly

The existing `agenttools.Registry` is the source of truth. A new helper `BuildToolCatalog(registry *agenttools.Registry) []llm.Tool` walks the registry, returning the internal `Tool` shape:

```go
type Tool struct {
    Name        string
    Description string
    Schema      json.RawMessage  // JSON Schema for input
    Permission  Permission       // R / W / X
}
```

Each provider adapter translates this to its native shape. The catalog is built once per loop iteration and passed to `ChatRequest.Tools`. The `Permission` tier is not sent to the provider — it is used agent-side for gating.

### Agent loop

`internal/agent/toolloop.go` is the new entry point that replaces the cloud-side of `ProcessRequest` when tools are enabled. Pseudocode:

```
loop_iteration = 0
consecutive_errors = 0
history = persisted_history(conv_id) + [user_message]

while loop_iteration < MAX_LOOP_ITERATIONS (default 10):
    catalog = BuildToolCatalog(registry)
    response = provider.StreamChat(ctx, { Messages: history, Tools: catalog })

    assistant_blocks = []
    tool_use_blocks = []

    for event in response:
        switch event.Type:
            case text_delta:    accumulate into current text block; emit to client
            case tool_use_start: open a tool_use block; emit folded UI entry
            case tool_use_delta: accumulate input json (do not render partial)
            case tool_use_stop:  finalize block; emit args summary to client
            case message_stop:   break

    assistant_blocks = collected blocks
    history.append({ role: assistant, blocks: assistant_blocks })

    if no tool_use_blocks:
        # model produced plain text only — turn complete
        persist_turn(history)
        return

    # partition by permission tier
    r_tier = [b for b in tool_use_blocks if registry.get(b.name).permission == R]
    w_tier = [b for b in tool_use_blocks if registry.get(b.name).permission == W]
    x_tier = [b for b in tool_use_blocks if registry.get(b.name).permission == X]

    # decide which require confirmation under current mode
    needs_confirm = []
    match permission_mode:
        case strict:     needs_confirm = w_tier + x_tier
        case permissive: needs_confirm = x_tier
        case bypass:     needs_confirm = []

    # R-tier runs concurrently
    r_results = await parallel(execute(b) for b in r_tier)

    # W/X serialize so confirm prompts see one at a time
    w_x_results = []
    for b in (w_tier + x_tier):
        if b in needs_confirm:
            send PermissionRequired event to client
            decision = await client decision (AllowToolCall | DenyToolCall)
            if decision == deny:
                w_x_results.append(tool_result(b.id, "user denied execution", is_error=True))
                persist_turn(history + w_x_results)
                return  # HARD turn-end on denial
        result = execute(b)
        w_x_results.append(result)

    all_results = r_results + w_x_results
    history.append({ role: user, blocks: all_results })

    # count consecutive errors
    if all_results are all errors:
        consecutive_errors += 1
        if consecutive_errors >= 3:
            persist_turn(history)
            emit notice("aborting: 3 consecutive tool errors")
            return
    elif any successful result:
        consecutive_errors = 0

    loop_iteration += 1

# hit cap
persist_turn(history)
emit notice("aborting: hit max tool loop iterations (10)")
```

`MAX_LOOP_ITERATIONS` is configurable in `~/.config/cercano/config.yaml` under `tool_loop.max_iterations`, default 10.

### Permission modes and the confirm gate

Three explicit modes:

| Mode | R-tier | W-tier | X-tier |
|---|---|---|---|
| `strict` | silent | confirm | confirm |
| `permissive` *(default)* | silent | silent | confirm |
| `bypass` | silent | silent | silent |

The mode is session-scoped, stored on the agent side, defaulted from `~/.config/cercano/permissions.yaml`. Setting the mode mid-session is an RPC: `SetPermissionMode(mode)`. Mode changes do not retroactively prompt for completed work.

The CLI's existing confirm-prompt UI (built in `internal/cli/ui/confirm_test.go` and `model.go`) is repointed: instead of firing on the local `/tool` invocation path, it fires on receipt of a `PermissionRequired` streaming event from the agent. The keystroke handlers (`y`/`n`/`d`/`esc`) generate an `AllowToolCall(tool_use_id)` or `DenyToolCall(tool_use_id)` RPC reply.

The mode indicator lives in the CLI status bar so the active floor is always visible. Slash commands `/strict`, `/permissive`, `/bypass` (and `/mode <name>`) RPC the agent.

### Concurrency model

R-tier tools in one assistant turn execute concurrently via `sync.WaitGroup` over goroutines. Order of results is preserved by indexing into a result slice. W- and X-tier serialize because the confirm gate is human-paced and must see one call at a time.

If the provider's `Capabilities().SupportsParallelTools` is false (some Ollama models that only emit single tool_calls), the agent serializes even R-tier calls. This is rare for the models we care about.

### Streaming and UI

The streaming RPC (`ProcessRequestStream`) carries a tagged event union. New event types added for tool calling:

- `ToolUseStart { tool_use_id, tool_name }`
- `ToolUseInputProgress { tool_use_id }` — emitted at small intervals while args build up; no payload other than "still streaming"
- `ToolUseStop { tool_use_id, args_summary }` — full args available
- `ToolExecStart { tool_use_id }` — spinner on
- `ToolExecComplete { tool_use_id, status, summary, is_error }` — spinner off, line updates
- `PermissionRequired { tool_use_id, tool_name, args, tier }` — pause for confirmation
- `Notice { text }` — already exists; reused for cap-hit and 3-strike messages

The CLI renders each tool call as a folded scrollback entry that defaults to one line:

```
▸ read_file path="main.go"            ✓ 32 lines
```

`▸` toggles to `▾` when expanded (full args JSON + full result text). Expansion state is per-entry, tracked client-side. Spinner is a small braille dot during exec.

### Persistence

Add `content_json TEXT` to `turns`:

```sql
ALTER TABLE turns ADD COLUMN content_json TEXT;
```

For tool-calling turns the agent writes the Anthropic block-array shape directly to `content_json` and leaves `content` empty:

```json
[
  {"type":"text","text":"I'll read main.go first."},
  {"type":"tool_use","id":"toolu_01...","name":"read_file","input":{"path":"main.go"}},
  {"type":"tool_result","tool_use_id":"toolu_01...","content":"...","is_error":false}
]
```

Text-only turns keep using `content` as today. The store reader prefers `content_json` when populated, falls back to `content`. No data migration needed.

`/resume` rehydrates by reading rows in order, deserializing `content_json` into `[]Block`, and dropping the resulting `[]Message` straight into the next `ChatRequest.Messages`. Scrollback rendering walks the same blocks and emits the same folded UI entries as a live turn.

### Error handling inside the loop

| Failure | What the agent does |
|---|---|
| Tool exec error, bad args, file not found | `tool_result` block with `is_error: true` and the error message as `content`. Loop continues. |
| User pressed n or esc on a confirm prompt | `tool_result` block with `is_error: true` and content `"user denied execution"`. **Hard turn-end** — no further loop iterations. |
| 3 consecutive loop iterations where every tool call in that iteration failed | Abort loop. Emit `Notice` with the failure chain. Persist what happened. If even one tool call in an iteration succeeded, the counter resets. |
| Loop-iteration cap (`MAX_LOOP_ITERATIONS`, default 10) hit | Abort loop. Emit `Notice` "hit max tool loop iterations". Persist. |
| Provider rate-limit or network error | Existing `degradeIfCloudFailure` path applies: retry on local provider with a `Notice` to the user. If local also lacks tool support, the request fails with a clear error. |
| Provider returns `SupportsTools=false` but loop requires tools | Fail the request before the loop starts with an actionable error: "configured model does not support tool calling; switch model or run without tools". |

Tool-result blocks must immediately follow the corresponding tool_use blocks in the message history with no other content between (per Anthropic's contract). The agent enforces this when assembling the user-role message that carries results.

### Capability advertisement

A new RPC `GetProviderCapabilities() -> {tools, parallel_tools, caching, vision, max_tools_per_call}` lets clients reflect what the current provider supports. The CLI uses it to hide tool-related UI affordances when running against a non-tool-capable local model.

### Agent / CLI split

| Concern | Lives in |
|---|---|
| Provider interface + Anthropic/Ollama adapters | Agent |
| Internal Message/Block/Tool types | Agent |
| Tool-loop control flow (iterate, terminate, count errors) | Agent |
| Tool catalog assembly from `agenttools.Registry` | Agent |
| Concurrent R-tier dispatch, serialized W/X-tier | Agent |
| Permission-mode storage and gate decision | Agent |
| Conversation persistence with `content_json` | Agent |
| `PermissionRequired` / `AllowToolCall` / `DenyToolCall` RPC events | Agent (events) / Client (rendering + response) |
| Confirm prompt UI (modal, key handling, diff reveal) | CLI |
| Folded scrollback tool-call entry, expand/collapse | CLI |
| Spinner during exec | CLI |
| Mode indicator in status bar | CLI |
| `/strict` `/permissive` `/bypass` slash commands | CLI (RPC the agent) |

This split keeps tool-calling capability available to every client unchanged. VS Code and Zed render `PermissionRequired` as native IDE notifications and reply with `AllowToolCall` / `DenyToolCall` over the same gRPC contract. Nothing tool-call-related is duplicated between clients.

### Migration of existing code

- `internal/llm/langchain.go` becomes `internal/llm/anthropic/client.go`, `adapter.go`, `stream.go`. langchaingo is removed as a dependency from the cloud path. (The local-only `LocalModelProvider` path stays on its current direct-Ollama wiring for now.)
- `internal/agent/agent.go` keeps `ProcessRequest` for the SmartRouter local-only no-tools path. New `internal/agent/toolloop.go` handles tool-enabled requests.
- `internal/cli/ui/model.go` confirm-prompt handling is repointed from the local `/tool` invocation path to streaming `PermissionRequired` events. Existing tests in `confirm_test.go` are updated to drive the agent-event path instead of the local model state directly.
- The `/tool` slash command remains as a developer-facing way to invoke tools by name without going through the model. It now also flows through the agent's confirm gate so behavior is consistent.
- `conversation.Store.Append` gains a `BlocksJSON` parameter alongside the existing `Content`. Old call sites pass empty `BlocksJSON`; the new tool-loop persistence path passes the marshaled block array.

### Testing strategy

- Unit tests per adapter: Anthropic adapter against `httptest.Server` returning canned SSE event streams (text, tool_use, mixed, error). Ollama adapter against canned NDJSON streams. Round-trip block adapter tests in both directions.
- Tool-loop unit tests using a `mockProvider` that returns scripted sequences: text-only, single tool call, parallel tool calls, tool error → retry, user-denial, consecutive errors, round-trip cap.
- Persistence: round-trip a tool-calling turn through `Store.Append` → `Store.GetTurns` → block array deserializes byte-identical.
- CLI tests: existing `confirm_test.go` rewritten to drive `PermissionRequired` events; new test verifies folded entry rendering and expand/collapse state.
- Integration: a `cercano agent` running against a fake-Anthropic httptest server, with a CLI client driving a full turn including a tool call and a confirm prompt.

## Status

Design approved 2026-06-21. Implementation plan written (see `native_tool_calling_tasks.md`) and executed.

- [x] Internal types (Block / Message / Tool / ToolChoice / Permission / StreamEvent / StreamReader / Provider / Capabilities / ChatRequest / ChatResponse)
- [x] Anthropic adapter (anthropic-sdk-go v1.51, WithBaseURL → Meridian, custom UA RoundTripper, Chat + StreamChat with SSE → StreamEvent translation, schema validation + Required pass-through)
- [x] Ollama adapter (ollama/api v0.30.10, Chat + StreamChat with NDJSON, ordered-map ToolCallFunctionArguments + ToolPropertiesMap)
- [x] Tool catalog assembly (`agenttools.BuildToolCatalog` + exported `PermissionToLLM`)
- [x] Bounded autonomous tool loop with R-tier concurrency, W/X serialization, 3-strike guard, iteration cap, user-deny hard turn-end
- [x] Permission modes (strict / permissive / bypass) stored agent-side via `PermissionStore` (~/.config/cercano/permissions.yaml)
- [x] PermissionRequired streaming events + AllowToolCall/DenyToolCall unary RPCs paired via `PendingDecisions`
- [x] Persistence with `content_json` column on `turns` (PRAGMA-guarded migration)
- [x] CLI confirm UI repointed to streaming events (RPC-based Allow/Deny)
- [x] CLI slash commands /strict /permissive /bypass /mode and palette-aware mode chip in status bar
- [x] CLI folded tool-call scrollback entries (expand/collapse stubbed; V1 renders folded)
- [x] gRPC handlers: SetPermissionMode, GetPermissionMode, AllowToolCall, DenyToolCall, GetProviderCapabilities
- [x] Legacy provider files relocated to `internal/legacymodels` (unblocks `agent → llm` import direction)
- [x] Main wiring: Anthropic provider constructed when `cfg.CloudProvider == "anthropic"`, wired via `srv.SetCloudLLMProvider`
- [x] Integration test: full tool-call turn through Anthropic adapter against httptest server
- [x] Supersedes `docs/plans/dispatch.md` for V1; references updated in `docs/plans/cli.md`

V1 follow-ups recorded for future work:
- `streamProcessRequestWithToolLoop` doesn't yet persist turns, propagate `WorkDir`/system prompt, or run the project-context loader. Needed before legacy `ProcessRequestStream` path can be deleted entirely.
- `/tool` invoking W/X-tier tools requires `/bypass` mode because the unary InvokeTool RPC can't surface the confirm prompt. Documented in `/tool` help.
- Expand/collapse keybind for folded tool entries is V2 polish.
- Legacy provider files in `internal/legacymodels/` await deletion once Google/etc. cloud paths are migrated or removed.
