# Watchdog — Design

**Part of:** [Agent Capabilities](../README.md) · the enforcement layer of
[Spec 0b Part C](../steering-protocol-substrate/design.md). This is the buildable
design that reconciles the original 0b Part C sketch with the pieces since built
(the dispatch engine's routing seam, the `review` capability's structured verdict,
and `agent.RunToolLoop` as the hook point).

**Depends on:** `agent.RunToolLoop` (the main tool loop), the dispatch engine's
`OneShot` + structured-verdict pattern, and the routing seam. Nothing new is
required — the router's "fast" model class is hardcoded until the matrix-router
upgrade lands.

## Goal

An independent supervisor that enforces workflow protocols instead of trusting the
main model to police itself — the answer to *silent* protocol-skipping. It watches
the main loop at decision points, and when it detects a likely skip it **challenges**
the model to comply or justify, converting silent skips into on-the-record decisions.

## Core principle

The watchdog is a small, fast, **router-resolved** model wrapped in deterministic
supervision logic. It does not trust and it is not trusted blindly: a cheap
deterministic pre-filter decides *whether* to consult it at all, and its failures
never block real work (fail-open). Every protocol it enforces is a pluggable `Check`,
so v1 ships one check end-to-end and the rest slot in unchanged.

## Architecture

`internal/watchdog` holds **pure supervision logic** — a registry of `Check`s, the
pre-filter, verdict handling, the intervention state machine, and the `justify` tool.
It has **no model client of its own**: it runs a check by calling an injected
one-shot dispatch (the dispatch engine's `OneShot` at the router's "fast" class) that
returns a `review`-style structured verdict.

It hooks into `agent.RunToolLoop` as a **second, orthogonal gate**. `RunToolLoop`
already has the R/W/X permission gate; the watchdog is a *protocol* gate that runs
alongside it, **independent of permission mode** (it supervises even under `bypass`).

- `ToolLoopInput` gains an optional `WatchdogGate` seam (a callback). `RunToolLoop`
  never imports `internal/watchdog`; the **server** constructs the watchdog with a
  dispatch handle + config and injects the callback — the same seam pattern used for
  the agentic runner and the permission requester.
- The callback fires **before a W/X tool call executes** and **at turn boundaries**
  (for text checks). It returns `allow` / `challenge` / `block`.

### Model selection

The watchdog asks the **router** for a model of the "fast"/lightweight class — it
expresses intent ("a cheap model for a supervisory check"), the router resolves it.
Today that default is hardcoded; when the embedded model×capability matrix router
lands, the watchdog gets a smarter resolution for free with no design change. The
check itself rides the dispatch `OneShot` path and parses a structured verdict, so
only the *model selection* differs from a normal one-shot.

## The `Check` abstraction

Each protocol enforcement is a self-contained `Check`:

```go
type Action struct {
    Kind       string          // "tool_call" | "turn_end"
    ToolName   string          // for tool_call: "edit_file", "write_file", ...
    ToolArgs   json.RawMessage // for tool_call
    Text       string          // for turn_end: the assistant text
    Transcript []llm.Message   // recent conversation for context
}

type Verdict struct {
    Violation bool   `json:"violation"`
    Protocol  string `json:"protocol"`  // e.g. "debug-loop"
    Challenge string `json:"challenge"` // one-line note shown to the main model
}

type Check interface {
    Name() string
    Applies(a Action) bool // deterministic pre-filter — no model
    Evaluate(ctx context.Context, a Action, oneShot OneShotFunc) (Verdict, error)
}

// OneShotFunc is the injected fast-model dispatch (engine OneShot, "fast" class),
// returning the model's raw completion text (parsed into a Verdict by the check).
type OneShotFunc func(ctx context.Context, prompt string) (string, error)
```

- **`Applies`** is the cheap pre-filter (pure Go). For `debug-loop`: "is this an
  `edit_file`/`write_file`/destructive tool call?"
- **`Evaluate`** either calls `oneShot` for a model judgment **or resolves
  deterministically** and skips the model. This keeps model-backed checks
  (debug-loop, plain-english) and purely-mechanical ones (a future `checkpoint`
  "N edits, no commit") in the same registry.

The watchdog holds a **registry** of checks; on a gated action it runs `Applies`,
then `Evaluate` for the matches. The first violation wins → intervention.

### v1 check — `debug-loop`

`Applies` = `edit_file`/`write_file`/destructive tool call. `Evaluate` builds a prompt
handing the fast model the recent transcript + the proposed edit and asking, in
effect: *"Is the agent editing to fix a bug or test failure with no evidence of the
debug loop (reduced the case, observed real data, confirmed the root cause)? If so,
return a one-line challenge."* → structured verdict.

Other protocols become additional `Check`s later with zero machinery change:
`plain-english` (a `turn_end` check on the assistant text), `decision-protocol`,
`checkpoint` (deterministic).

## Intervention flow (challenge / justify / escalate)

On a `challenge` verdict:

1. **Challenge** — the proposed action is **not executed**. The loop injects a
   synthetic tool_result on that call: *"⚡ watchdog: <challenge>. Either follow the
   protocol first, or call `justify` with a reason to override."* The model re-plans.
2. **The model self-declares** one of three things:
   - **Complies** — runs the protocol, then re-issues the action. The transcript now
     has evidence, so the watchdog re-evaluates and returns `allow` → it executes.
   - **Justifies** — calls a small watchdog-owned **`justify(reason)`** tool (offered
     during a challenge). The override is logged, this line of work is marked
     protocol-satisfied-by-override (not re-challenged), and the re-issued action
     proceeds. Scrollback: `⚡ watchdog override — proceeded without debug loop ·
     reason: "obvious typo"`.
   - **Re-skips** — re-issues the same action with neither compliance nor `justify`.
     The watchdog increments a per-(action, protocol) repeat counter.
3. **Escalate** — when the repeat counter crosses `escalate_after` (default 2), the
   watchdog surfaces to the human (the deadlock/misfire safety valve), the way the
   permission gate already prompts. Only then is a person involved.

The `justify` **tool** (not a text tag) is chosen because it's structured and
unambiguous to act on — consistent with the structured-verdict direction, no fragile
parsing, and it fits the tool loop.

**Hard-block (strict mode, opt-in):** same verdict path, but the synthetic result
says *"blocked — run the debug loop first"* with no `justify` offered. For
zero-tolerance contexts; autonomous runs auto-enable it.

**State:** the watchdog keeps small per-conversation state — which (action, protocol)
pairs are justified-or-satisfied (don't re-challenge) and the repeat counters. An
"action" for identity/counting purposes is keyed by `(protocol, tool name, a
normalized hash of the tool args)`, so re-issuing the *same* edit is recognized as a
repeat while an unrelated later edit is not.

## Configuration

A `watchdog` config block:

- `enabled` — **default off** (new + potentially noisy; opt-in). A `/watchdog` slash
  toggle can come later.
- `mode` — `challenge-and-justify` (default) vs `strict` (hard-block). Autonomous runs
  auto-enable `strict`.
- `checks` — active checks (v1: `["debug-loop"]`).
- `model` — the router "fast" class (hardcoded small default for now).
- `escalate_after` — repeat threshold before surfacing to the human (default 2).
- `echo` — **default off**, a debug/observability setting. When on, the watchdog emits
  its full exchange to the user's scrollback via the existing `RunToolLoop` event
  stream, **labeled by thread**: `watchdog:` (the pre-filter hit, the fast model's
  verdict + reasoning, the injected challenge) and `main:` (the main model's response
  — its `justify` reason, or that it complied). It's a pure observability layer on the
  event sink — no behavior change — for debugging *why* the watchdog challenged or
  allowed something.

## Error handling — never wedge the main loop

If the fast model is unreachable/errors, or the verdict won't parse, the gate
**fails open**: the action proceeds and a warning is logged. Supervision degrading is
always preferable to blocking real work on a supervisor hiccup. (This mirrors the 0b
"pre-filter fails open" note.)

## Data flow

```
main loop proposes a W/X tool call (or reaches turn end)
  → WatchdogGate fires (if watchdog enabled)
      → for each Check: Applies? → Evaluate (deterministic or oneShot fast model)
          → Verdict.Violation?
              no  → allow → normal permission gate → execute
              yes → challenge-and-justify mode:
                      inject synthetic result; do NOT execute; model re-plans
                        complies  → re-evaluate → allow
                        justify   → log override, mark satisfied, allow re-issue
                        re-skip   → counter++ → escalate at threshold
                    strict mode:
                      inject "blocked"; no justify; force protocol
  (echo on → emit the labeled watchdog:/main: exchange to scrollback throughout)
```

## Testing

The machinery is fully testable with a **stubbed one-shot verdict** — independent of
any real small model:
- Check unit tests: `Applies` matches the right actions; `Evaluate` returns the parsed
  verdict from a stubbed `oneShot`.
- Intervention flow: drive `RunToolLoop` with a fake provider scripting the three paths
  — (a) challenge → `justify` → action proceeds + override logged; (b) challenge →
  comply (adds evidence) → re-evaluate allows; (c) challenge → re-skip → escalate at
  the threshold.
- Fail-open: stubbed `oneShot` returns an error → the action proceeds.
- The `WatchdogGate` seam in `RunToolLoop` tested with a fake gate (no `watchdog`
  package dependency).
- `echo` on: assert the labeled `watchdog:`/`main:` events reach the event sink.

## Wiring

`internal/watchdog` (Check registry + gate state machine + the `justify` tool). The
server builds it with a dispatch one-shot handle (`OneShot` at the "fast" class) +
the `watchdog` config, and injects `WatchdogGate` into `ToolLoopInput`. The `justify`
tool is granted to the loop only while the watchdog is enabled.

## Dependencies & extensibility

- Builds on already-shipped pieces: `RunToolLoop`, dispatch `OneShot` + structured
  verdict, the routing seam. The router "fast" class is hardcoded until the
  matrix-router upgrade.
- New checks (`plain-english`, `decision-protocol`, `checkpoint`) are added by
  implementing `Check` and registering it — no machinery change.
- The protocol bodies the checks reason about come from `internal/protocols` (0b).

## Out of scope (here)

- The remaining protocol checks beyond `debug-loop` (they're the extensibility
  target, added incrementally).
- The embedded matrix-router (the "fast" class is hardcoded for now).
- A `/watchdog` slash command / UI beyond the scrollback echo.
