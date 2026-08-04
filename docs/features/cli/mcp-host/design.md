# MCP Host

> Status: Built — phases 6 + 15. **Validated end-to-end** against a live
> third-party stdio server (`rekolektion-viz`): `AddMcpServer` connected it, 22
> tools registered under `mcp__rekolektion-viz__*` and reached `ready`, and
> `InvokeTool` round-tripped real results back through the agent.

## Using it — adding an MCP server

An MCP server is declared in `~/.config/cercano/mcp.yaml`:

```yaml
mcpServers:
  rekolektion-viz:
    command: /path/to/dotnet
    args:
      - run
      - --project
      - /path/to/Rekolektion.Viz.Mcp
    env: {}
```

Three ways to register / manage one:

- **The MCP config tab (primary UI)** — `/config` → the **MCP** tab (or bare
  `/mcp`, which opens straight to it) is a live dashboard of every hosted
  server: name, state (`connecting` / `ready` / `failed`), tool count, and last
  error, refreshing so a `connecting → ready` transition appears without a
  reload. Per-row keys: `r` reconnect, `x` remove. Press `a` to float an
  add-server popover form (name / command / args / env) — `enter` submits via
  `AddMcpServer`, `esc` cancels. This is the way to add/manage a server without
  memorizing command syntax.
- **`/mcp` fast paths (CLI)** — `/mcp add <name> <command> [args…]`,
  `/mcp remove <name>`, `/mcp restart <name>` call the same RPCs directly for
  scripted or muscle-memory use. (Bare `/mcp` and `/mcp list` open the config
  tab above rather than printing a table.)
- **Config file** — edit `mcp.yaml` and (re)start the agent; declared servers
  connect in the background at boot.

Live changes (from the tab or `/mcp add`) persist the entry to `mcp.yaml` in
canonical form.

Registered tools appear in `/tools` as `mcp/<server>/<tool>` (display form; the
model sees `mcp__<server>__<tool>`) and, being untrusted third-party code,
confirm by default even in `permissive` mode until allowlisted (the `[a]lways
allow` confirm key persists an allowlist entry).


## Overview / Goal

### Problem

Cercano can be *consumed* as an MCP server (Claude Code calls `cercano_*` tools over
stdio), but the standalone agent cannot *host* external MCP servers itself. A user
running the Cercano CLI/agent has no way to plug in third-party MCP servers
(github, filesystem, a company-internal tool server, etc.) and have the model call
their tools the way it calls the built-in `Read`/`Write`/`Bash` suite.

This is phase 6 ("MCP host runtime") + phase 15 ("MCP UI") of the CLI track, both
previously deferred. The MCP client SDK is already vendored
(`github.com/modelcontextprotocol/go-sdk v1.3.1` — the same dependency the server
side uses), so no new dependency is required.

### Key insight

The agent's tool surface is the `agenttools.Registry` — a dynamic, thread-safe,
duplicate-checked map of `Tool` implementations. `BuildToolCatalog` flattens
whatever is registered into the LLM tool catalog; the tool loop partitions calls
by R/W/X tier and gates W/X through the permission system. An MCP-hosted tool is
therefore **just another `Tool` implementation** whose `Execute` proxies a
`tools/call` JSON-RPC request to an external server and whose `Schema()` is the
server's advertised input schema. Register it and it flows through the catalog,
`/tools`, and the permission gate for free.

**Consequence: the tool loop and the LLM provider layer do not change at all.**
This feature is a new `internal/mcp_host/` package, a registry-population step at
boot, one change to the permission gate, an allowlist extension, four new RPCs,
and a CLI `/mcp` command.

### Goals

1. The agent connects to external MCP servers declared in config, enumerates their
   tools, and registers each into the shared tool registry so the model can call
   them in normal chat — no tool-loop or provider changes.
2. External MCP tools are **untrusted third-party code** and confirm by default,
   even in `permissive` mode. A name-based allowlist (`permissions.yaml`) promotes
   specific tools/servers to silent; `bypass` mode skips everything.
3. Server lifecycle is agent-owned and observable. Boot is non-blocking; tools
   appear per-server as each connects; a call to a not-yet-ready server blocks only
   that call.
4. The CLI gets `/mcp list|add|remove|restart` and surfaces MCP tools in `/tools`.
   The CLI stays thin — parse, call RPC, render. All state lives agent-side, so
   VS Code / Zed inherit the same behavior.

### Non-goals (this slice)

- **Per-project / per-workspace server sets.** Servers are global to the machine
  for now. (The "project" abstraction may be wrong for Cercano — users work across
  multiple projects from one client; a "workspace" concept may replace it later.
  Out of scope here.)
- **Trusting MCP tool annotations to reduce friction.** `readOnlyHint` /
  `destructiveHint` are server-self-reported and are **not** used to lower the
  confirmation bar (matches Claude Code / Cursor). A destructive hint is surfaced as
  a display-only ⚠ marker.
- **SSE / HTTP MCP transports.** stdio only in v1.
- **Cached tool manifests** that would let tools be advertised before a server
  connects (a future widening of lazy-connect).

## Design / Approach

### Decisions (load-bearing)

| Decision | Choice | Rationale |
|---|---|---|
| Where MCP tools live | Register into the shared `agenttools.Registry` | Registry is dynamic + thread-safe; tool loop stays MCP-agnostic |
| Server scope | Global only (`~/.config/cercano/mcp.yaml`) | "Project" model unproven; workspace concept deferred |
| Trust default | Confirm even in `permissive`; allowlist promotes to silent; `bypass` skips | External code; matches Claude Code / Cursor field standard |
| Annotations | Ignored for gating; destructive shown as ⚠ only | Spec says hints are untrusted; field ignores them |
| Tool name to model | `mcp__server__tool` (double underscore) | Anthropic API rejects `/` in tool names; `/` form is display-only |
| Config | `mcp.yaml` canonical (mcpServers logical schema); auto-import `.mcp.json` | YAML matches house convention; JSON import for portability |
| Boot | Non-blocking; per-server registration; per-call wait on not-ready server | Slow/dead server never blocks the agent or unrelated tools |

### Package layout — `source/server/internal/mcp_host/`

| File | Responsibility |
|---|---|
| `client.go` | One MCP client per server over stdio (go-sdk). `connect` / `listTools` / `callTool` / `close` / health |
| `manager.go` | Lifecycle: spawn configured servers (background), supervise, restart, stop. Owns `map[server]*client` and per-server state |
| `tool.go` | `mcpTool` implements `agenttools.Tool`. `Execute` proxies `tools/call`; `Schema()` = server's inputSchema; `Origin()` = `OriginMCP` |
| `config.go` | Load `mcp.yaml` + import `.mcp.json`; schema structs; map both to one internal shape |

### Tool naming

- **To the model / in the catalog / in the allowlist:** `mcp__<server>__<tool>`
  (Anthropic-safe `^[a-zA-Z0-9_-]+$`).
- **In CLI display (`/tools`, `/mcp list`):** `mcp/<server>/<tool>`, with a ⚠
  marker when the tool reports a destructive hint.

### Registration (the only non-gate wiring change)

At boot, after `agenttools.DefaultRegistry()` is built and attached via
`SetToolRegistry`, the manager connects to each configured server in the
background. As a server finishes `tools/list`, each of its tools is wrapped as an
`mcpTool` and `Register`ed into the same registry. They then flow through
`BuildToolCatalog`, `/tools`, and the gate unchanged. `/mcp restart` re-registers
live (unregister old set → register fresh set, atomically per server).

### Lifecycle & failure handling

- **Boot is non-blocking.** `manager.Start()` launches all server connections
  concurrently; the agent serves immediately. Fast servers come up first.
- **Per-server registration.** A server's tools enter the catalog the moment that
  server lists — independent of the others.
- **Per-call, per-server block.** If the model invokes an MCP tool whose server
  isn't ready (warming or reconnecting), *that one `Execute`* waits on that
  server's readiness (with a `waiting for mcp server '<name>'…` message) up to a
  timeout. Ready servers' tools run instantly; a warming/dead server never blocks
  unrelated tools or the loop.
- **Dead / failed server.** Registers nothing; reported as `failed` with its error
  in `/mcp list`. The model is never offered a tool that can't run.
- **Per-call failure / timeout.** `Execute` returns a clean tool error
  (`mcp server '<name>' unavailable — /mcp restart <name>`) fed back as a
  `tool_result`. No hang, no agent crash.
- **Mutations.** `/mcp add` / `remove` edit `mcp.yaml` then start/stop that one
  server live — no agent restart. `/mcp restart` stops → respawns → re-lists →
  swaps its tools atomically.

### Permission gate & allowlist (the one behavioral change)

`mcpTool` carries `Origin() == OriginMCP` (an `Origin` concept added to the `Tool`
interface; built-ins return `OriginBuiltin`). This avoids string-sniffing tool
names at gate time. Gate logic, evaluated per W/X call:

1. mode `bypass` → allow silently (unchanged)
2. tool is **MCP** and name matches an allow rule in `permissions.yaml` → allow
   silently
3. tool is **MCP** and *not* allowlisted → **require confirm**, regardless of
   `permissive` (the "confirm by default" override)
4. otherwise → existing mode/tier behavior (built-in W silent in `permissive`, etc.)

Built-ins are untouched; only MCP tools get the "always confirm until allowlisted"
treatment.

**Allowlist storage.** `permissions.yaml` gains an MCP allow section of glob rules:
`mcp__github__create_issue` (one tool), `mcp__github__*` (whole server),
`mcp__*` (all). `PermissionStore` gains `IsMCPAllowed(toolName)`.

**`[a]lways allow`.** The confirm prompt for an MCP tool adds an `[a]` key beyond
`[y]`/`[n]`. Choosing it appends the exact `mcp__server__tool` rule to
`permissions.yaml`, persists, allows this call, and runs silent thereafter. The
persist intent flows back through the existing `AllowToolCall` RPC plus a
"persist" flag.

### Config — `~/.config/cercano/mcp.yaml`

YAML is canonical; `.mcp.json` (Claude Code shape) is auto-imported on load and
mapped to the same internal shape. Both use the same logical schema — a map of
server name → `{ command, args, env }`.

```yaml
# ~/.config/cercano/mcp.yaml
mcpServers:
  github:
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      GITHUB_TOKEN: ${GITHUB_TOKEN}
  filesystem:
    command: mcp-server-filesystem
    args: ["/Users/me/work"]
```

### RPCs (`source/proto/agent.proto`)

The MCP-host RPCs deferred in phase 1:

| RPC | Purpose |
|---|---|
| `ListMcpServers` | name, state (ready/warming/failed), tool count, error |
| `AddMcpServer` | append to `mcp.yaml`, spawn, list, register |
| `RemoveMcpServer` | stop, unregister tools, drop from `mcp.yaml` |
| `RestartMcpServer` | stop → respawn → re-list → atomic re-register |

`/tools` needs no new RPC — MCP tools are in the registry, so `ListTools` already
returns them (closing the phase-15 `/tools` gap).

### CLI surface — `source/clients/cli/internal/slash/`

- `/mcp` or `/mcp list` → Table of servers (name, state, #tools, error) via the
  existing Table primitive.
- `/mcp add <name> <command> [args…]`, `/mcp remove <name>`,
  `/mcp restart <name>` → call the RPCs, render result.
- Confirm prompt gains the `[a]` key for MCP tools; forwarded via the existing
  `AllowToolCall` path + persist flag.

CLI stays thin: parse → call RPC → render. Lifecycle and state live agent-side.

While the agent auto-launches, the CLI shows a `Loading MCP servers…` status line;
detailed per-server status is available via `/mcp list` once connected.

## Testing

- **`mcp_host` client:** go-sdk in-memory transport — stand up a mock MCP server
  in-process; assert `tools/list` + `tools/call` round-trip, error/timeout paths.
- **Config:** `mcp.yaml` parse, `.mcp.json` import, schema mapping, env expansion.
- **Gate:** MCP tool confirms in `permissive`; allowlisted tool runs silent;
  `bypass` skips; `[a]lways allow` appends to `permissions.yaml` and persists
  across reload. Built-ins unchanged (regression assert).
- **Registry/catalog:** registered `mcp__server__tool` surfaces in
  `BuildToolCatalog` + `ListTools`.
- **Manager lifecycle:** restart swaps tools atomically; a failed server registers
  nothing and reports `failed`; per-call wait-then-timeout on a not-ready server.
- **RPC handlers:** list/add/remove/restart against a fake manager.

## Open Questions / Notes

- **Workspace concept.** Global-only is a deliberate v1 simplification. Revisit a
  "workspace" abstraction (multiple projects under one client context) before
  adding per-project server sets.
- **`Origin` on the `Tool` interface.** Adds a method to every built-in. Trivial
  (`return OriginBuiltin`), but it is an interface change — alternative is a
  name-prefix check at the gate, rejected as fragile.
- **Per-call wait window.** A tool is only callable once its server has listed, so
  in the normal path the per-call wait rarely triggers — it covers the
  reconnect/in-flight edge. Timeout value TBD during implementation (likely a few
  seconds).
