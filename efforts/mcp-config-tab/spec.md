# MCP config tab

## Problem

Managing hosted MCP servers today is only possible through the `/mcp` slash
command (`list` / `add` / `remove` / `restart`). Its output is a one-shot text
dump into scrollback — not a live, managed view. There is no discoverable place
in the UI to see server state, tool counts, and errors at a glance, and adding a
server means remembering the exact `add <name> <cmd> [args…]` syntax on one
line.

The `/config` surface already gathers every other configurable subsystem
(General, Cloud, Runtime, Models, UI, Context) into one tabbed page. MCP servers
belong there too.

## Goal

Add an **MCP** tab to the `/config` tabbed surface: a live, refreshing dashboard
listing every hosted MCP server with its state, tool count, and last error, plus
per-row actions to **reconnect** and **remove**, and an **add-server popover
form** for registering a new one without memorizing command syntax.

`/mcp` (bare) becomes a shortcut that opens this tab directly, instead of
dumping a table into scrollback. The `add` / `remove` / `restart` subcommands of
`/mcp` stay as fast paths for scripted / muscle-memory use.

## Scope

This is **entirely CLI-side**. The four gRPC RPCs
(`ListMcpServers` / `AddMcpServer` / `RemoveMcpServer` / `RestartMcpServer`) and
their `agentclient.Client` wrappers already exist and are validated end-to-end.
No proto, server, or client-package changes.

### In scope

- A new `configTabMcp` tab in the `/config` surface, labelled **MCP**, slotted
  after **Models** (strip reads `General · Cloud · Runtime · Models · MCP · UI ·
  Context`).
- An `mcpDashboard` content page (implements `contentPage` +
  `contentPageScroller`) modelled on `runtimeDashboard`:
  - Rows: one per server — name, state (`ready` / `connecting` / `failed`),
    tool count, error (if any).
  - Periodic refresh tick (re-fetches `ListMcpServers`) so `connecting →
    ready` transitions and tool counts update live.
  - Cursor navigation over rows.
  - Per-row key actions: `r` reconnect (`RestartMcpServer`), `x` remove
    (`RemoveMcpServer`).
  - `a` opens the add-server popover.
  - Empty state when no servers are configured, with a hint to press `a`.
- An **add-server popover form** overlay (name / command / args / env) composited
  over the dashboard via the existing `composeOverlay` compositor. On submit it
  calls `AddMcpServer` and refreshes the list; Esc cancels.
- `/mcp` (bare, i.e. the former `list`) opens this tab via a new
  `ResultOpenMcpConfig` result kind, rather than emitting a scrollback table.
- Digit-jump in `handleConfigSurfaceKey` widened to cover 7 tabs.

### Out of scope

- Any change to the MCP host runtime, RPCs, permission model, or `mcp.yaml`
  schema.
- Editing an existing server's command/args in place (remove + re-add covers
  it for V1). May be a later enhancement.
- Per-tool visibility / allowlist management inside the tab (that lives in the
  confirm flow + `/tools`).
- `.cercano/mcp.yaml` per-project vs global scope selection in the add form
  (V1 writes through `AddMcpServer`, which targets the user config as today).

## User experience

- `/config` → arrow to **MCP** tab, or `/mcp` opens straight to it.
- The tab lists servers as rows. The selected row is highlighted.
- Footer hint shows the row actions: `a add · r reconnect · x remove`.
- Pressing `a` floats a bordered popover form in the center. Fields: **name**,
  **command**, **args** (space-separated), **env** (optional `K=V` pairs).
  Enter submits, Esc cancels. On success the popover closes and the row for the
  new server appears (initially `connecting`, flipping to `ready` on the next
  refresh tick).
- `r` on a row reconnects it (row shows a transient pending state, then
  refreshed status). `x` removes it (row disappears after refresh).
- Errors from any RPC surface as an inline action message on the page (same
  pattern the runtime dashboard uses), not a crash.

## Acceptance criteria

1. `/config` shows a 7th tab labelled **MCP** between **Models** and **UI**;
   ←/→, Tab/Shift+Tab, and digit-jump all reach it.
2. The MCP tab lists all hosted servers with name / state / tool count / error,
   and refreshes live (a `connecting` server flips to `ready` without manual
   reload).
3. `r` on a selected row reconnects that server; `x` removes it; both reflect
   in the list after refresh.
4. `a` opens a centered popover form; submitting a valid name+command adds the
   server (verified against `ListMcpServers`) and closes the popover; Esc
   cancels with no change.
5. Bare `/mcp` opens the MCP config tab (no scrollback table); `/mcp add|remove
   |restart` still work as before.
6. Empty-server state renders a clear "no servers — press a to add" message.
7. `go test ./...` passes in the CLI module, including new unit tests for the
   tab wiring, the dashboard row/action model, the popover form state machine,
   and the `/mcp` → `ResultOpenMcpConfig` dispatch.
