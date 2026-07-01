# Developer Settings Section (watchdog toggles) — Design

**Part of:** [Agent Capabilities](../README.md) · completes the watchdog
[visibility increment](increment-2-design.md). Increment 2 built the echo
rendering; this exposes the switches that control it. Lands on the same
`watchdog-visibility` branch.

## Goal

Add a **Developer** section to the CLI settings page with two toggles —
**Watchdog enabled** and **Echo watchdog conversation** — that persist to the
server config and take effect live.

## Why both toggles

`echo` only produces output when the watchdog is enabled (the per-turn echo
wiring sits inside `if s.watchdog != nil`). The watchdog is default-OFF, so an
echo toggle alone would be inert on a fresh install. The Developer section
therefore carries both, making it self-sufficient: turn the supervisor on and
watch it, all from settings.

## Architecture — full-stack config plumbing

`enabled`/`echo` live in the server's `config.Watchdog`, reached from the CLI via
the existing `GetConfig` / `UpdateConfig` gRPC. Four layers change.

### Proto (`source/proto/agent.proto`, regenerated)

- `GetConfigResponse`: add `bool watchdog_enabled = 12;` and
  `bool watchdog_echo = 13;` (non-sparse response — always carries the current
  value).
- `UpdateConfigRequest`: add `string watchdog_enabled` and
  `string watchdog_echo` (next free tags), using the existing **sparse-string**
  convention — `""` means "leave unchanged", `"true"`/`"false"` set it. This
  matches how every other `UpdateConfigRequest` field already works (empty =
  unset); no proto3 `optional` needed.
- Regenerate `agent.pb.go` with the pinned toolchain (protoc v7.34.1,
  protoc-gen-go v1.36.11), verifying the diff is only the new fields.

### agentclient (`pkg/agentclient/client.go`)

- `Config`: add `WatchdogEnabled bool`, `WatchdogEcho bool`, populated in
  `GetConfig` from the response.
- `ConfigUpdate`: add `WatchdogEnabled string`, `WatchdogEcho string`
  (`""`=unchanged), mapped onto the proto request in `UpdateConfig`.

### Server (`internal/server/server.go`)

- `GetConfig`: populate the two response bools from `currentConfig.Watchdog`
  (under `cfgMu`).
- `UpdateConfig`: for each of the two fields, if the patch value is non-empty,
  parse `"true"`/`"false"` and apply to `currentConfig.Watchdog.{Enabled,Echo}`
  (under `cfgMu`), then `persistConfig()`. If either changed, call
  `InitWatchdog()` to **rebuild the watchdog** so an `enabled` flip
  builds/tears-down the supervisor live. (`echo` is already read live per-turn;
  the rebuild is harmless for it.) An unrecognized value (not
  `""`/`"true"`/`"false"`) is ignored, consistent with the tolerant patch
  handling of the existing fields.

### CLI (`internal/ui/settings_build.go`)

- `buildSettingsSections`: append a section
  `{Title: "Developer", Fields: [...]}` with
  `form.NewToggle("watchdog-enabled", <label>, cfg.WatchdogEnabled)` and
  `form.NewToggle("watchdog-echo", <label>, cfg.WatchdogEcho)`.
- `classifyCommit`: add `case "watchdog-enabled":` and `case "watchdog-echo":`
  that set the matching `ConfigUpdate` string field to the committed toggle
  value, returning `commitAction{kind: commitConfig, update: u}` — reusing the
  existing commit → `UpdateConfig` sink. No new UI machinery.

## Live behavior

- Toggle **Watchdog enabled** → `UpdateConfig` rebuilds the watchdog → the next
  turn it's active (challenges/echo flow) or inert. Persists to the config file.
- Toggle **Echo watchdog conversation** → next turn echoes (or stops) the
  labeled `watchdog:`/`main:` lines. Persists.

## Isolation

Each layer has one job and a clear seam: proto = the wire contract; agentclient
= typed client mapping; server = apply + rebuild + persist; CLI = present two
toggles and route their commits. Each is independently testable.

## Testing

- **Server:** `UpdateConfig` with `watchdog_enabled="true"` and
  `watchdog_echo="true"` applies to `currentConfig.Watchdog`, rebuilds the
  watchdog (non-nil after enable), and persists; `GetConfig` returns the two
  bools. A `"false"` patch tears it back down. Mirror the existing
  `TestUpdateConfig_*` tests.
- **agentclient:** `Config` populates the two bools from a response;
  `ConfigUpdate` maps the two strings onto the request.
- **CLI:** `buildSettingsSections` includes a "Developer" section with the two
  toggles reflecting `cfg` values; `classifyCommit` maps both keys to a
  `commitConfig` action carrying the right `ConfigUpdate` field.

## Out of scope / follow-ons

- **A dedicated toggle-switch UI component.** First pass uses the existing
  `form.NewToggle` (checkbox-style). A nicer on/off switch widget is a later
  UI-polish task.
- Exposing the other watchdog config (checks list, mode, model, escalate_after)
  in settings — not needed yet.
