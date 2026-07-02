# Watchdog settings completeness (B') — Design

**Part of:** [Agent Capabilities](../README.md) · completes the watchdog controls
in the settings page. The [developer-settings](developer-settings-design.md)
increment exposed `enabled` + `echo`; this adds the rest of the meaningful
`WatchdogConfig` knobs so the whole supervisor is controllable from settings
(replacing the need for a `/watchdog` slash command).

## Goal

Expose **mode**, **per-check on/off**, and **escalate-after** in the Developer
settings section, persisted to the server config and applied live — alongside
the existing enabled/echo toggles.

## What's exposed (and what isn't)

`WatchdogConfig` = `Enabled, Echo` (done) + `Mode, Checks, EscalateAfter` (this
increment) + `Model` (omitted — advanced; the fast lane is hardcoded for now).

- **Mode** → a select: `challenge-and-justify` / `strict`.
- **Checks** → one toggle per known check (`debug-loop`, `commit-checkpoint`,
  `plain-english`); each reflects membership in the active `Checks` list.
- **Escalate after** → a validated number (text field; positive int; default 2).

## Architecture — extends the dev-settings config path

Same four layers as the dev-settings increment (`GetConfig`/`UpdateConfig`
gRPC), with the same live-rebuild via the lock-free `buildWatchdogFrom`.

### Proto (`source/proto/agent.proto`, regenerated)

- `GetConfigResponse` (non-sparse, current values): add
  `string watchdog_mode`, `string watchdog_checks` (comma-joined), and
  `string watchdog_escalate_after` (the int as a string). Next free tags after
  the dev-settings fields.
- `UpdateConfigRequest` (sparse): add `string watchdog_mode`,
  `string watchdog_checks`, `string watchdog_escalate_after` — `""` = unchanged,
  matching the established convention.

### agentclient (`pkg/agentclient/client.go`)

- `Config`: add `WatchdogMode string`, `WatchdogChecks []string` (split from the
  response's comma-joined string), `WatchdogEscalateAfter int`.
- `ConfigUpdate`: add `WatchdogMode string`, `WatchdogChecks string` (the
  encoded value; see below), `WatchdogEscalateAfter string` — mapped onto the
  request.

### Server (`internal/server/server.go`)

- `GetConfig`: emit `Mode`, `strings.Join(Checks, ",")`, and
  `strconv.Itoa(EscalateAfter)` from `currentConfig.Watchdog`.
- `UpdateConfig` (under the held `cfgMu` write lock, before the existing
  watchdog rebuild): for each non-empty field, apply to
  `currentConfig.Watchdog`:
  - `watchdog_mode`: set `Mode` (accept only `"challenge-and-justify"` /
    `"strict"`; ignore other values).
  - `watchdog_escalate_after`: `strconv.Atoi`; apply only on a valid `>= 1`
    parse (ignore invalid — tolerant, like the other fields).
  - `watchdog_checks`: `""` = unchanged; the **sentinel `"-"`** = empty list
    (`[]string{}`); otherwise split on `,` and trim. (The sentinel resolves the
    empty-vs-unchanged collision a bare comma-join can't.)
  - Any of these changing sets the existing `watchdogChanged` flag, so the
    already-present `s.watchdog = s.buildWatchdogFrom(...)` rebuild + persist
    fires. (Mode/checks need the rebuild; escalate_after is read at
    construction, so it too needs the rebuild — the existing flag covers all.)

### CLI (`internal/ui/settings_build.go`)

- The Developer section gains, after the enabled/echo toggles:
  - `form.NewSelect("watchdog-mode", <label>, [challenge-and-justify, strict], cfg.WatchdogMode)`
  - a `form.NewToggle("watchdog-check-<name>", <label>, <name ∈ cfg.WatchdogChecks>)`
    for each name in a package-level `knownWatchdogChecks =
    {"debug-loop","commit-checkpoint","plain-english"}` (kept in sync with the
    server's check-map — a code comment notes this).
  - `form.NewText("watchdog-escalate-after", <label>, strconv.Itoa(cfg.WatchdogEscalateAfter), "")`
- **Commit routing:** `classifyCommit` handles `watchdog-mode` and
  `watchdog-escalate-after` directly (set the matching `ConfigUpdate` string).
  For a `watchdog-check-<name>` toggle, the new full `Checks` list is computed
  from the current `cfg.WatchdogChecks` ± that check — so `classifyCommit` (or
  the settings-page commit path) must have access to the current checks. It
  gains the current `[]string` as an argument; it returns a `ConfigUpdate` whose
  `WatchdogChecks` is the comma-joined new list, or the `"-"` sentinel when the
  new list is empty.

## Data flow (a check toggle)

```
user flips the plain-english toggle off
  → settings computes newChecks = cfg.WatchdogChecks without "plain-english"
  → ConfigUpdate{WatchdogChecks: "debug-loop,commit-checkpoint"}  (or "-" if empty)
  → UpdateConfig: split → currentConfig.Watchdog.Checks; rebuild watchdog; persist
  → next turn: the rebuilt watchdog runs only the remaining checks
  → GetConfig now reports the updated list; the toggle reflects it
```

## Error handling

- Tolerant, matching the dev-settings path: an unrecognized `mode`, an
  unparseable/`< 1` escalate-after, or a malformed checks string is **ignored**
  (that field left unchanged) rather than erroring the whole update.
- The rebuild reuses the deadlock-safe `buildWatchdogFrom` (no `cfgMu`
  re-entry) and the race-guarded `s.watchdog` snapshot from the dev-settings
  increment — no new concurrency surface.

## Testing

- **Server:** `UpdateConfig` applies `watchdog_mode="strict"`,
  `watchdog_escalate_after="3"`, and a `watchdog_checks` list (incl. the `"-"`
  empty sentinel) → `currentConfig.Watchdog` updated + watchdog rebuilt +
  persisted; invalid mode / non-numeric escalate ignored; `GetConfig` reports
  all three. Mirror the dev-settings `TestUpdateConfig_Watchdog*` tests.
- **agentclient:** `Config` splits the checks string into a slice + maps mode /
  escalate; `ConfigUpdate` maps the three onto the request.
- **CLI:** the Developer section includes the mode select, a toggle per known
  check reflecting membership, and the escalate text field; `classifyCommit`
  maps mode/escalate directly and computes the new checks list (add + remove +
  empty→`"-"`) from the current config.

## Out of scope / follow-ons

- The `model` override (advanced; hardcoded co-processor lane for now).
- A dedicated toggle-switch / number-stepper UI widget (first pass reuses
  `form.NewToggle` / `form.NewText`, matching the existing settings page).
- Prose-ifying settings labels (all fields use their kebab keys today).
