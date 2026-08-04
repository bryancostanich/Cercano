# Plan — MCP config tab

All work is in the CLI module (`source/clients/cli/`). No proto / server /
`agentclient` changes: the four RPCs and the `McpServer{Name,State,ToolCount,
Err}` struct already exist and are validated.

Reference implementations to mirror:
- Tab wiring: `internal/ui/config_tabs.go`, `config_surface.go`.
- Live per-row-action page: `internal/ui/runtime_dashboard.go` (implements
  `contentPage` + `contentPageScroller`, refresh tick, action rows, async
  action cmds, inline action message).
- Popover overlay compositing: `internal/ui/overlay.go` (`composeOverlay`) and
  modal precedent `internal/ui/open_runtime_modal.go`.
- Slash → UI dispatch: `internal/slash/registry.go` result kinds,
  `internal/slash/theme.go` (`ResultOpenThemeSettings`), and the switch in
  `internal/ui/model.go` (~line 2563).

## Phase 1 — Register the MCP tab (skeleton, no popover)

- [x] Add `configTabMcp` to the `configTab` enum in `config_tabs.go`, ordered
      after `configTabModels` and before `configTabUI`. Add its label "MCP" to
      `configTabLabels` at the matching index; bump `configTabCount` 6 → 7.
- [x] In `config_surface.go` `buildConfigTabPage`, add a `case configTabMcp:`
      that constructs the new `mcpDashboard` and batches its refresh tick
      (mirrors the `configTabModels` case).
- [x] In `handleConfigSurfaceKey`, widen the digit-jump case from
      `"1"…"5"` to `"1"…"7"` so all tabs are reachable by number.
- [x] Update `config_tabs_test.go` expectations for the new label list, count,
      and any index-based assertions. Confirm `configTabFromID` /
      `clampConfigTab` round-trip the new tab.
- [x] Build + run `go test ./internal/ui/...`; tab strip renders 7 tabs.

## Phase 2 — The mcpDashboard content page

New file `internal/ui/mcp_dashboard.go` (+ `mcp_dashboard_test.go`).

- [x] Define `mcpDashboard` struct: agent client, palette/styles, width/height,
      `servers []agentclient.McpServer`, cursor, `actionMessage string`, and a
      `popover *mcpAddForm` (nil when closed — Phase 3).
- [x] Implement `contentPage`: `ID()` (add `contentPageMcp` to
      `content_page.go`), `SetSize`, `Update`, `View`.
- [x] Implement `contentPageScroller`: `ScrollBy` / `ScrollTo` / `ScrollState`
      (thin — row list is usually short; reuse the runtime dashboard's clamp
      pattern).
- [x] Snapshot loading: `loadMcpSnapshotCmd` calls
      `client.ListMcpServers(ctx)` off the UI goroutine, delivers an
      `mcpSnapshotMsg`. Add a `refreshTick()` (tea.Tick) like the runtime
      dashboard so state/tool-count update live. Wire msg handling in the root
      model's Update where other dashboard msgs are handled.
- [x] Row rendering: name · state · tool count · error, selected row
      highlighted. Empty state: "no MCP servers — press a to add".
- [x] Footer hint: `a add · r reconnect · x remove`.
- [x] Key handling in `Update` (when no popover open):
      - `up`/`down` (and `k`/`j`) move the cursor, clamped.
      - `r` → async `RestartMcpServer(name)` cmd; set a pending action message;
        refresh on completion.
      - `x` → async `RemoveMcpServer(name)` cmd; same pattern.
      - `a` → open the popover (Phase 3).
      RPC errors set `actionMessage`, never panic.
- [x] Tests: cursor nav + clamp; row render for ready/connecting/failed/empty;
      that `r`/`x` emit the right async cmd for the selected server; that a
      snapshot msg replaces the row list and re-clamps the cursor.

## Phase 3 — Add-server popover form

Same file or `internal/ui/mcp_add_form.go` (+ test).

- [x] Define `mcpAddForm`: fields name / command / args / env, a focused-field
      index, and text-input state. Reuse whatever text-input primitive the
      existing modals use (check `open_runtime_modal.go` for the field widget);
      fall back to a minimal local field if none is shared.
- [x] `View()` renders a bordered box (lipgloss border) of fixed visible width,
      sized to content, with field labels + values and a hint line
      (`enter add · esc cancel`).
- [x] `Update(key)` handles field focus movement (Tab / ↑ / ↓), text editing,
      `enter` (validate name+command non-empty → emit an
      `mcpAddSubmitMsg{name,command,args,env}`), and `esc` (cancel → close).
- [x] In `mcpDashboard.View`, when `popover != nil`, compute centered (x, y)
      from width/height and splice via `composeOverlay(base, form.View(), x, y)`.
- [x] In `mcpDashboard.Update`, when `popover != nil`, route keys to the form
      first; on submit call `AddMcpServer(ctx, name, command, args, env)` async,
      close the popover, refresh; on cancel just close.
- [x] Parse `args` by whitespace and `env` as `K=V` tokens → `map[string]string`
      (ignore malformed env tokens with an inline note).
- [x] Tests: form field navigation; validation rejects empty name/command;
      enter emits the submit msg with parsed args/env; esc closes without a
      submit; a successful add closes the popover and triggers a refresh.

## Phase 4 — /mcp opens the tab

- [x] Add `ResultOpenMcpConfig` to the `ResultKind` enum in
      `internal/slash/registry.go`.
- [x] In `internal/slash/mcp.go`, change the `list` (and bare `/mcp`) branch to
      return `Result{Kind: ResultOpenMcpConfig}` instead of building the text
      table. Keep `add` / `remove` / `restart` returning `ResultText` as today
      (fast paths).
- [x] In `internal/ui/model.go`, add `case slash.ResultOpenMcpConfig:` →
      `m.openConfigSurface(configTabMcp)` (mirror `ResultOpenThemeSettings`).
- [x] Update `internal/slash/mcp_test.go`: bare `/mcp` and `/mcp list` now
      return `ResultOpenMcpConfig`; add/remove/restart unchanged.

## Phase 5 — Verification & docs

- [x] `go test ./...` green in the CLI module.
- [ ] Manual smoke: launch agent, `/config` → MCP tab, add the rekolektion-viz
      server via the popover, watch it go `connecting → ready` with tool count,
      reconnect it, remove it; confirm bare `/mcp` lands on the tab.
- [x] Update `docs/features/cli/mcp-host/design.md` "adding an MCP server"
      section to mention the `/config` MCP tab + popover as the primary UI, with
      `/mcp add` as the CLI fast path.
- [x] Checkpoint per phase with conventional-commit messages.

## Risks / notes

- The root model's Update must learn the new snapshot/action msg types; find
  where `runtimeDashboardSnapshotMsg` and friends are dispatched and add the
  parallel `mcp*` cases so the tick loop stays alive.
- Keep the popover width within the terminal; `composeOverlay` won't truncate,
  so pick (x, y) defensively for narrow terminals (clamp like the other modals).
- `configTabCount` is referenced by wrap-around and digit-jump; changing it in
  one place (the const) is enough, but grep for any hard-coded `6` or `'5'`
  bound (the digit-jump case is the known one).
