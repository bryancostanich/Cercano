# Spec 0a — Unified Capability Architecture + Migration

**Part of:** [Agent Capabilities](../README.md) · Tier 0, Foundation
**Depends on:** nothing (this is the base layer)
**Blocks:** Spec 0b (steering substrate) and all Tier 1/2 work that exposes tools on
both surfaces.

## Goal

A capability — a thing Cercano can do — is written **once** and lights up **both**
as a standalone built-in tool (in Cercano's own agent loop) **and** as an MCP plugin
tool (`cercano_<name>`) for host agents. One shared core, no duplicated logic.

## Vocabulary

| Term | Meaning |
|---|---|
| **Tool** | One callable function the model invokes — `Read`, `Bash`, `plan`. Has a name, parameters, and code that runs. |
| **MCP tool** | The same idea, exposed over MCP to a host agent as `cercano_<name>`. |
| **Capability** | The underlying logic of a thing Cercano can do, independent of how it's exposed. A tool and an MCP tool are two **ways to expose** one capability. |
| **Surface** | A place a capability is exposed: the standalone **agent** loop, or the **MCP** plugin. |

## Problem (current state)

The agent server has three parallel implementations of overlapping logic:

1. **Standalone agent loop** — `internal/agenttools/`, the `agenttools.Tool` interface
   (`Name`/`Description`/`Permission`/`Schema`/`Execute`), 15 tools registered in
   `DefaultRegistry()`, consumed by `internal/agent/toolloop.go`.
2. **Dispatch loop** — `internal/dispatch/`, a separate `dispatch.Tool` interface with
   its own builtins (`read_file`, `write_file`, `shell`, …) that overlap the agenttools.
   Backs the `cercano_dispatch` MCP tool's local agentic loop.
3. **MCP handlers** — `internal/mcp/server.go`, `gomcp.AddTool` handlers for the
   co-processor tools (`summarize`, `extract`, `classify`, `explain`, `fetch`,
   `research`, `document`, `deep_research`, …). Most proxy back into the agent over the
   gRPC `AgentClient`.

Three interfaces, overlapping logic copied across them. Adding a capability that works
everywhere means writing it up to three times.

## Design

### The Capability interface

One definition per capability, in a new `internal/capabilities/` package:

```go
type Capability interface {
    Name() string                        // canonical snake_case id: "read_file", "plan"
    Description() string
    Tier() Tier                          // R / W / X — declarative permission level
    Schema() Schema                      // parameters, defined once
    Surfaces() Surface                   // bitmask: agent | mcp | both (default both)
    Execute(ctx context.Context, call *Call) (*Result, error)
}
```

- **`Name`** is the single canonical id. Display names per surface come from an alias
  map (see Naming).
- **`Tier`** is declarative metadata (R = read/silent, W = write/confirm,
  X = destructive/always-confirm). It does not enforce anything itself; each surface's
  gate reads it (see Permissions).
- **`Surfaces`** lets a capability opt out of a surface — e.g. a standalone-only tool
  excludes `mcp`. Default is both.
- **`Schema`** is defined once and reused to advertise parameters to the model on each
  surface and to validate inbound args.

### Dependencies (Services) and per-call context (Call)

Capabilities need shared services and per-invocation context. These are kept separate.

**`Services`** — injected once when the registry is built. The static collaborators a
capability may need:

```go
type Services struct {
    CloudProvider llm.Provider          // may be nil (local-only deployments)
    LocalProvider llm.Provider
    Engine        engine.InferenceEngine
    Config        *config.Config        // pkg/config.Config
    Conversations conversation.Store
    ProjectCtx    *projectctx.Loader    // internal/context, imported as projectctx
    // ... extended as new capability kinds need more
}
```

Note: there is no `ProviderSet` type today — the agent server holds `cloudLLMProvider`
and `localLLMProvider` as two discrete `llm.Provider` fields and selects per-request via
Locus Mode. `Services` mirrors that. A capability that needs "the active provider" gets a
helper (`Services.MainProvider(isCloud)`) rather than a set.

**`Call`** — per-invocation state, constructed by the adapter for each tool call:

```go
type Call struct {
    Args        json.RawMessage      // validated against Schema
    WorkDir     string
    ConversationID string
    RequestPermission func(ctx, PermissionAsk) (bool, error) // surface-provided gate hook
    Emit        func(Event)          // streaming events back to the surface
}
```

`Execute` therefore sees static services (via the registry/closure) and dynamic call
context (via `*Call`), and never reaches for globals. A capability that needs the LLM
calls `services.Providers`; one that needs to stream progress calls `call.Emit`; one
that needs to ask the user calls `call.RequestPermission` (which each surface wires to
its own mechanism).

### Registry

One `capabilities.Registry`, constructed once with `Services`, holds every capability.
It is the single source of truth that both adapters read from. API mirrors today's
`agenttools.Registry`: `Register`, `Get`, `All`, filtered by surface.

### Adapters (written once, generic over any capability)

Two adapters wrap **any** capability — they are not written per-capability.

**Agent adapter** (`internal/capabilities/agentadapter`):
- Presents the registry's `agent`-surface capabilities to `toolloop.go` as the tools it
  already consumes. Either by implementing the existing `agenttools.Tool` interface over
  a `Capability`, or by changing the loop to consume the registry directly. (Chosen
  approach: implement `agenttools.Tool` over `Capability` so `toolloop.go` is untouched
  in 0a; a later cleanup can collapse the interface.)
- Maps canonical name → standalone display name via the alias table (preserves the
  deliberate Claude-style `Read`/`Write`/`Bash` names).
- Wires `Call.RequestPermission` to the existing `PermissionRequester` gate and
  `Call.Emit` to the existing streaming-event channel.
- Enforces `Tier` through the existing permission gate + modes
  (strict / permissive / bypass).

**MCP adapter** (`internal/capabilities/mcpadapter`):
- Registers each `mcp`-surface capability as a `cercano_<name>` tool via
  `gomcp.AddTool`, deriving the schema from `Schema()`.
- Each handler is a thin forwarder to a single generic gRPC RPC,
  `InvokeCapability(name, argsJSON) → resultJSON`, served by the agent server. The agent
  resolves the capability from the shared registry and runs `Execute` with `Services`.
  The MCP server and agent server keep their existing gRPC boundary — the same boundary
  that lets the MCP server target a remote `cercano agent`. The N bespoke proxy handlers
  (each hand-building a prompt and calling `ProcessRequest`) collapse into this one path:
  the duplicated logic is gone even though the transport stays.
- `Tier` is declared as metadata; the host's own permission system does the gating.
  `Call.RequestPermission` resolves to a no-op (allow) since Cercano does not double-gate
  inside someone else's agent.

### New gRPC RPC: `InvokeCapability`

The recon confirmed the MCP server holds no in-process handle to the agent — even in
embedded `--mcp` mode it dials the agent over gRPC on a loopback listener
(`mcpserver.NewServer(grpcClient)` gets only a `proto.AgentClient`), and in external
mode it targets a remote `cercano agent`. Rather than restructure that wiring, unification
adds one generic RPC the agent serves:

```
rpc InvokeCapability(InvokeCapabilityRequest) returns (InvokeCapabilityResponse);

message InvokeCapabilityRequest  { string name = 1; bytes args_json = 2; string work_dir = 3; }
message InvokeCapabilityResponse { bytes result_json = 1; bool is_error = 2; string error = 3; }
```

This is deliberate: the gRPC server-comm architecture is foundational for longer-term
goals (remote agents, multiple clients sharing one agent). Unification removes duplicated
*logic*, not the transport.

### Naming across surfaces

- One canonical snake_case id per capability (`read_file`, `plan`).
- Per-surface display names via an alias map:
  - **agent** surface keeps Claude-style names (`Read`, `Write`, `Edit`, `Bash`) for
    planning ergonomics (the model's training favors these).
  - **mcp** surface uses `cercano_<canonical>` (`cercano_read_file`).
- One implementation, two labels — never two implementations.

### Permission tiers across surfaces

`Tier` is declarative. Enforcement differs by surface, which is correct:

- **Standalone:** Cercano owns the loop and the user relationship, so it enforces tiers
  via its own modes + gate. R runs silently; W/X go through the confirm gate unless the
  mode auto-approves.
- **Plugin:** the host (Claude Code) owns the user relationship and has its own
  permission model. Cercano declares the tier (so a host *could* use it) but does not
  gate — gating inside another agent's loop would double-prompt and fight the host.

## Migration (full, behavior-preserving)

This sub-project migrates everything onto the Capability model now — no lingering
duplication.

### What moves

1. **The 15 standalone built-ins** (`internal/agenttools/`: `Read`, `Write`, `Edit`,
   `Bash`, `Grep`, `Glob`, `LS`, `stat_file`, `git_status`, `git_log`, `git_add`,
   `git_commit`, `rm_file`, `git_push`, `git_reset_hard`) → become capabilities. The
   agent adapter re-exposes them under their current display names, so the standalone
   loop sees no behavioral change.
2. **The dispatch builtins** (`internal/dispatch/builtin/`: `read_file`, `write_file`,
   `shell`, …) → these overlap the agenttools and collapse into the **same**
   capabilities. `dispatch.Tool` is retired; the dispatch loop consumes a **subset** of
   the shared registry through the agent adapter. One read-file implementation total.
3. **The MCP co-processor handlers** (`summarize`, `extract`, `classify`, `explain`,
   `fetch`, `research`, `document`, `deep_research`) → become capabilities, exposed via
   the MCP adapter. Each is invoked through the generic `InvokeCapability` RPC instead of
   its own bespoke `ProcessRequest`-with-a-hand-built-prompt handler. The gRPC boundary
   stays; the per-handler duplication does not.

### What stays as-is (deliberately not forced into the model)

- **Control-plane tools** — `config`, `models`, `stats`, `init`, `skills`. These are
  management, not model-invoked work; they remain MCP tools / RPCs.
- **`dispatch`** stays for now. It is the subagent engine (Tier 2) and will fold into the
  subagent capability there, not in this foundation.
- **The gRPC server-comm architecture** stays — for both the CLI ↔ agent split and the
  MCP ↔ agent split (the latter also enables pointing the MCP server at a remote
  `cercano agent`). This is deliberate and foundational for longer-term goals.
  Unification removes duplicated *logic*, not the transport: the bespoke per-tool proxy
  handlers are replaced by one generic `InvokeCapability` RPC.
- **Provider layer, conversation store, SmartRouter, context meter** — untouched.

## Data flow

**Standalone tool call:**
```
model emits tool_use ("Edit")
  → toolloop.go partitions by tier
  → agent adapter resolves "Edit" → capability "edit_file"
  → builds *Call (args, workdir, RequestPermission=gate, Emit=stream)
  → tier gate (W) → PermissionRequester → user confirm
  → Capability.Execute(ctx, call) using Services
  → *Result → tool_result block → back to model
```

**Plugin (MCP) tool call:**
```
host calls cercano_summarize
  → mcp adapter validates args against Schema
  → forwards to agent via InvokeCapability("summarize", argsJSON) over gRPC
  → agent resolves "summarize" in the shared registry
  → builds *Call (args, workdir, RequestPermission=allow, Emit=progress)
  → Capability.Execute(ctx, call) using Services
  → resultJSON → MCP CallToolResult → back to host
```

## Error handling

- **Arg validation** happens in the adapter against `Schema()` before `Execute`; invalid
  args return a structured tool error, never reach the capability.
- **Execute errors** become a tool error result on each surface (an errored
  `tool_result` block standalone; a `CallToolResult` with `isError` for MCP), preserving
  today's behavior where a tool error feeds back to the model rather than crashing the
  loop. The loop's existing guard (3 consecutive all-errored iterations aborts) is
  unchanged.
- **Permission denial** standalone is a hard turn-end, exactly as today.

## Testing

- **Per-capability unit tests** on `Execute` (given args + fake `Services`, assert
  `Result`).
- **Adapter conformance test** — a generic test that registers a sample capability and
  asserts it round-trips through both the agent adapter and the MCP adapter (name
  resolution, schema advertisement, arg validation, error mapping).
- **Behavior-pinning tests** — before migration, capture the current observable behavior
  of the 15 built-ins (golden inputs → outputs); after migration, the same tests pass.
  This makes the migration provably behavior-preserving, not a rewrite.
- **Full Go test suite** (`go test ./...`) green in both modules.

## Extension — Dispatch engine (co-processor + subagent), folded into 0a

This spec (Tasks 1–10) covers the capability foundation: the model, the 15-tool migration,
dispatch-loop consolidation, `InvokeCapability`, and the MCP adapter — all built. The
original **Task 11 (co-processor migration) is superseded** by the **dispatch engine**,
which is folded into 0a as extended phases (user decision). The dispatch engine treats
co-processor commands and subagent calls as one primitive ("delegated model work"), adds
a shared provider-boundary usage layer, surface-aware project-context injection, and a
`review` capability. Its full design — and how it pulls the Tier-2 subagent engine
forward and improves on the superpowers patterns — lives in
[`../dispatch-engine/design.md`](../dispatch-engine/design.md). An implementation plan for
the dispatch-engine phases still needs to be written, and its protocol/watchdog pieces
depend on Spec 0b.

## Out of scope (here)

- New *non-dispatch* capabilities (plan, brainstorm, protocols) — Spec 0b and Tier 1/2.
- Collapsing the `agenttools.Tool` interface entirely (kept as a thin alias in 0a to
  avoid touching `toolloop.go`; a later cleanup can remove it).
</content>
