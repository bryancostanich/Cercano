# Watchdog polish (increment C) — Design

**Part of:** [Agent Capabilities](../README.md) · the closing sweep of the
watchdog buildout. Every item below was logged as an accepted follow-on by one
of the five review gates (increments 1, 2, dev-settings, turn_end, settings
completeness). No new features — robustness fixes, test tightening, and
hygiene.

## 1. Robustness fixes (behavior changes)

### parseVerdict accepts decorated affirmatives

`parseVerdict` treats only an exact `yes`/`true` value as a violation, so a
fast local model that writes `VIOLATION: yes.` or `VIOLATION: yes (clearly)`
silently parses as *no violation* — under-enforcement with no signal. Fix:
prefix-match the value (`yes`/`true` prefixes count as affirmative). Genuine
ambiguity (no `VIOLATION:` line, other values) still fails open.

### Echo field data race

`Watchdog.SetEcho` writes the `echo` callback field unsynchronized while
`Gate`/`justify` read it via `emitEcho`. The server sets echo once per turn and
turns don't overlap today, so this is latent — but it's a real race under any
future concurrency, and the `SetEcho` doc comment over-claims ("safe to call on
a live Watchdog"). Fix, minimal by design: guard the field reads/writes with
the watchdog's existing mutex (`SetEcho` takes the lock; `emitEcho` snapshots
the callback under the lock, invokes it outside the lock — preserving the
"never call echo while holding w.mu" invariant) and correct the doc comment.
**Rejected as premature:** per-conversation echo routing — machinery nothing
needs under the single-active-turn server; cross-conversation echo isolation
remains a documented limitation.

### Check toggles compute from live form state

`classifyCommit` computes a toggled checks list from the cached
`sp.cfg.WatchdogChecks`. `onCommit` nils `sp.cfg` after every successful
commit and the next snapshot re-fetches — but if that re-fetch fails (server
briefly down), the next check toggle computes from nil and sends a
single-element list, silently dropping the other active checks server-side.
Fix: derive the current check states from the form's own toggle fields (the
`watchdog-check-*` toggles' displayed values) at commit time, so the
computation can never be stale. `classifyCommit` keeps its
`currentChecks []string` parameter; the call site builds that list from the
live fields instead of `sp.cfg`. (Exact mechanism — reading sibling field state
at commit — to be settled in the plan against the real `form` API; if the form
API can't expose sibling values cleanly, the fallback is passing a
`currentChecks` snapshot captured from the fields at form-build time and
updated on commit.)

## 2. Test tightening (no behavior change)

- `debugloop_test.go`: nil-`oneShot` → no-violation (fail-open) test — the
  check plain-english already has.
- `echo_test.go`: cover the `block` and `escalate` emit branches (only
  `challenge` is covered).
- `watchdog_loop_test.go`: turn_end `block` (reopens with the no-override
  message, does not return the flagged text) and `escalate` (emits the event
  and returns the reply) branch tests.
- `client_watchdog_test.go` (agentclient): drive the real `GetWatchdogEvent()`
  payload-loop branch end-to-end (a `StreamProcessResponse` in → the
  `TypeWatchdog` `StreamMsg` out), not just the mapping helper.
- `watchdog_render_test.go`: block test asserts the full
  "blocked — no override" phrase.
- `verdict_test.go`: the affirmative case asserts the challenge text is the
  model-emitted line, not the fallback (also covers the new prefix-match).

## 3. Dead code + doc hygiene

- Remove `editAction2()` (unused) from `commitcheckpoint_test.go`.
- Remove the `md := render.NewMarkdown(...)` / `_ = md` scaffolding from
  `watchdog_render_test.go`.
- `settings_build.go`: reword the `knownWatchdogChecks` comment — it references
  a "check-map"; the server side is a `switch` in `watchdog_wire.go`.
- `docs/features/agent-capabilities/watchdog/design.md`: add a short "v1
  behavior notes" paragraph — turn_end escalate is graceful (emit + return the
  reply; no human prompt), and the turn_end repeat counter keys on the exact
  reply text, so only verbatim-repeated output reaches `escalate_after` (the
  loop's iteration cap is the backstop for varied output).

## Out of scope

- Per-conversation echo routing (rejected above).
- Prose settings labels (kebab keys kept for page-wide consistency, per prior
  decision).
- `EscalateAfter==0` serializing as `"0"` in GetConfig (benign; apply path
  rejects `<1` and `watchdog.New` normalizes).
- Repo-wide gofmt drift in files this work never touched.

## Testing

Buckets 1–2 are inherently test-covered (each fix lands with its test; the
tightening items *are* tests). Full suites in both modules must stay green.
