# Watchdog Increment 2 — Visibility + commit-checkpoint check — Design

**Part of:** [Agent Capabilities](../README.md) · builds directly on the shipped
[watchdog](design.md) (0b Part C, the protocol-enforcement supervisor). That
increment shipped a working vertical slice — one check (`debug-loop`), the
`RunToolLoop` gate, config, and server wiring — but two gaps keep it from
earning its keep: **it's invisible** (the events it emits are dropped before
reaching the client) and **it fires rarely** (its one check only triggers on a
narrow situation). This increment closes both.

## Goal

Make the watchdog *observably* enforce a protocol you can reliably trigger:
render its interventions in the CLI, and add a second check — a **model-judged
commit-checkpoint** — that nudges the agent to commit a completed unit of work
before starting a different one.

## Two parts, one increment

1. **`commit-checkpoint` check** (server-only): a new pluggable `Check`.
2. **Client rendering** (proto + server sink + CLI): surface watchdog events in
   scrollback.

They're independent but ship together because the check gives you something to
*see* and the rendering is what lets you see it.

---

## Part 1 — the `commit-checkpoint` check

A new `Check` in `internal/watchdog`, registered under the name
`commit-checkpoint`. No machinery change — it plugs into the existing registry,
gate, and challenge/justify/escalate flow like `debug-loop`.

### Signal: a work *boundary*, never a count

The trigger is semantic, not numeric. "How many edits" is the wrong model — a
one-line fix and a ten-file refactor are both single units. The check nudges
when a *completed, uncommitted* unit of work is sitting there and the agent
begins a *different* one.

### Pre-filter (`Applies`, deterministic, no model)

Returns true only when **both**:
- the action is a code-mutating tool call (`edit_file` / `write_file` /
  `rm_file`), **and**
- the transcript contains at least one such edit since the most recent
  `git_commit` tool call (i.e. there is uncommitted work).

If there's no uncommitted work (or the last relevant action was a commit), the
check never consults the model. This is a cheap backward scan of
`Action.Transcript` for `git_commit` vs. edit tool calls.

### Judgment (`Evaluate`, fast model)

Hands the model:
- a summary of the edits made since the last commit (tool names + arg paths /
  short arg text extracted from the transcript), and
- the edit now beginning (`Action.ToolName` + `Action.ToolArgs`).

Asks exactly one question: *is the new edit a continuation of the same unit of
work, or the start of a different one — such that the prior uncommitted edits
form a complete, committable change?* A passing test/build visible in the
transcript is **context the model may weigh** as evidence a unit completed, but
is **never** an independent trigger (passing tests can be mid-work).

- Clear boundary → `VIOLATION: yes` + `CHALLENGE:` a one-line
  "commit the completed <prior work> before starting <new work>".
- Continuation, or any ambiguity → `VIOLATION: no` (conservative; fail-open —
  a noisy commit-nag is worse than a missed one).

Output is the same `VIOLATION:/CHALLENGE:` format the existing `parseVerdict`
already handles. A nil `oneShot` yields no violation (fail-open), matching
`debug-loop`.

### Intervention behavior (unchanged machinery)

The nudge is an ordinary `challenge`: the pending edit is held and a synthetic
tool-result tells the agent to commit or `justify`. The agent typically commits
(via its existing git-commit capability) then re-issues the edit — the re-eval
now sees a fresh `git_commit` in the transcript, so `Applies` is false and the
edit proceeds. Or it `justify`s to keep going. Repeat-skip → escalate, exactly
as today.

### Registration & config

Registered by name `commit-checkpoint`. Added to the **default checks list**
(`["debug-loop", "commit-checkpoint"]`) so enabling the watchdog activates both.
The watchdog remains default-**off** overall. No per-check config (no threshold
exists to configure — that's the point).

---

## Part 2 — client rendering

Today the server's event→client sink (`server.go`, the `sink func(agent.LoopEvent)`
switch) only maps tool-use/exec events to the gRPC stream; watchdog event kinds
fall through and are **silently dropped**. This part wires them through to the
TUI.

### Server

- Add a distinct `LoopWatchdogBlock` event kind in `internal/agent/toolloop.go`
  and emit it from the `block` branch (today `block` reuses
  `LoopWatchdogChallenge`; this closes that review finding so the client can
  distinguish challenge from block).
- Add sink cases for `LoopWatchdogChallenge`, `LoopWatchdogBlock`, and
  `LoopWatchdogEcho` that send the new proto message below. `escalate` stays on
  the existing permission-prompt path (already surfaced to the client) and is
  **not** part of this rendering.

### Proto

One new message on the `StreamProcessResponse` `oneof Payload`:

```
message WatchdogEvent {
  string kind      = 1; // "challenge" | "block" | "echo"
  string protocol  = 2; // e.g. "commit-checkpoint" (empty for echo)
  string text      = 3; // the challenge text, or the echo line
  string thread    = 4; // echo only: "watchdog" | "main" (empty otherwise)
}
```

Mapping from `LoopEvent`: `Summary` → `text`, `Detail` → `protocol` (for
challenge/block) or `thread` carried in `ToolName` for echo (per the T9 echo
wiring, which sets `ToolName: thread, Summary: text`). The sink normalizes these
into the fields above.

### CLI (Bubble Tea TUI, `source/clients/cli`)

Consume `WatchdogEvent` in the stream handler and render into scrollback:

- **challenge / block** → a set-apart callout with its own style (distinct from
  tool output), e.g.:
  ```
  ⚡ watchdog · commit-checkpoint
    The auth refactor looks complete and isn't committed — commit it before
    starting the parser work, or justify.
  ```
  `block` uses the same callout with a "(blocked — no override)" marker.
- **echo** (only arrives when `echo` is enabled server-side) → dim, labeled
  debug lines, visually secondary so they read as commentary:
  ```
  watchdog: commit-checkpoint → boundary shift, prior work uncommitted
  main: justify — "same change, one more file"
  ```

No new client config: events render whenever they arrive; echo events only
arrive when echo is enabled on the server.

---

## Architecture & isolation

- The check is a self-contained file in `internal/watchdog` implementing the
  existing `Check` interface — understandable and testable in isolation with a
  stubbed `oneShot`. It depends only on `Action.Transcript` + the fast model.
- The rendering path has three clear seams: the server sink (LoopEvent →
  proto), the proto contract (`WatchdogEvent`), and the CLI renderer (proto →
  scrollback line). Each is independently testable.

## Error handling

- Check: fail-open everywhere (nil oneShot → no violation; model error →
  surfaces as a check error which the Gate already treats as no-violation;
  ambiguous verdict → no nudge). A commit-nag must never wedge or spam.
- Rendering: a malformed/omitted `WatchdogEvent` field renders best-effort (a
  callout with empty protocol still shows the text); the client never blocks the
  turn on a render issue. Unknown `kind` values are ignored by the renderer.

## Testing

- **Check** (`internal/watchdog`): stubbed-`oneShot` unit tests — `Applies` is
  false with no uncommitted edits and false right after a `git_commit`; true
  with uncommitted edits + an edit action. `Evaluate` returns a nudge on a
  stubbed boundary verdict and stays quiet on a continuation verdict; nil
  oneShot → no violation.
- **Server sink** (`internal/server`): a `LoopWatchdogChallenge` and a
  `LoopWatchdogEcho` event each produce the expected `WatchdogEvent` payload on
  the stream (following existing sink tests).
- **CLI** (`source/clients/cli`): a golden/render test that a `WatchdogEvent`
  (challenge, and echo) produces the expected scrollback lines, following the
  existing scrollback golden tests.

## Out of scope (here)

- The `turn_end` gate path and text checks (`plain-english`) — a separate
  increment; the checks here all ride the existing W/X tool-call path.
- Per-check configuration / thresholds (none needed for these checks).
- A `/watchdog` slash toggle and the matrix-router "fast class" (still the
  co-processor lane).
- Multi-conversation echo isolation (safe under today's single-active-turn use).
