# Watchdog: turn_end gate + plain-english check — Design

**Part of:** [Agent Capabilities](../README.md) · extends the
[watchdog](design.md) (0b Part C). The watchdog so far gates only **tool calls**;
`Action.Kind` already allows `"turn_end"` but nothing emits it. This increment
wires the turn-boundary hook — unlocking a whole class of **text checks** — and
ships the first one: `plain-english`.

## Goal

Let the watchdog supervise the model's **final reply text**, not just its tool
calls: at each turn's end, judge the reply and, on a violation, send the turn
back for a rewrite. First check: `plain-english` — flag jargon, LLM/corporate
shorthand, talking-down, and over-hedging in favor of plain colleague-level
English.

## The turn_end gate (infrastructure)

`RunToolLoop` ends a turn at `toolloop.go:209` — the model returned `finalText`
with no tool calls. We add a **second, parallel seam** on `ToolLoopInput`,
leaving the working tool-call gate untouched:

```go
// WatchdogTurnEnd, when set, is consulted with the model's final reply text
// before the turn returns. nil = disabled. Fail-open: any error → the reply is
// returned unchanged.
WatchdogTurnEnd func(ctx context.Context, finalText string, transcript []llm.Message) WatchdogDecision
```

The server wires it to the same `watchdog.Gate` with
`Action{Kind:"turn_end", Text: finalText, Transcript: transcript}`. Two callbacks
(tool-call + turn-end) both adapt to one `Gate`; `RunToolLoop` never imports the
watchdog package (same seam pattern as increment 1).

## The intervention — reopen the turn

A text check can't "skip execution": the reply already exists. So the turn-end
intervention **reopens the turn** instead of blocking a call. At `toolloop.go:209`,
before returning `finalText`:

- **allow / nil gate / error** → return `finalText` as today (fail-open).
- **challenge** → do NOT return. Append a synthetic user turn to the history —
  *"⚡ watchdog (plain-english): &lt;challenge&gt;. Rewrite your reply in plain,
  colleague-level English, or call `justify` with a reason."* — emit a
  `LoopWatchdogChallenge` event, and **continue the loop** so the model produces
  a new reply. The model can comply (revise) or call the existing `justify`
  tool.
- **block** (strict) → same reopen, message says "rewrite required (no
  override)".
- **escalate** (repeat threshold crossed) → surface to the human via the
  existing `PermissionRequester` ("watchdog flagged the reply repeatedly — send
  as-is?"); allow→return the text, deny→reopen. No requester → fail-open (return
  the text).

The loop's existing **iteration cap** (50) bounds any rewrite cycle; the
`escalate_after` counter surfaces a stuck loop to the human well before that.

### justify / escalate identity at turn_end

`Gate` already handles `Action{Kind:"turn_end"}`. Its action-identity key
(`keyFor`) currently hashes `toolName + toolArgs`, both empty for turn_end. We
extend `keyFor` so a `turn_end` action keys on `protocol + "turn_end" +
hash(Text)`. Effect: re-issuing the *same* flagged reply is the repeat that
escalates (and the `justify` override, recorded against that key, lets the same
reply through), while an unrelated later reply is judged fresh. `Gate` already
sets `lastChallenged` on challenge/escalate regardless of kind, so `justify`
works unchanged.

## The plain-english check

`func PlainEnglishCheck() Check`, registered as `"plain-english"`.

- **`Applies`** (deterministic): `a.Kind == "turn_end"` and the reply is
  non-trivial (e.g. `len(strings.TrimSpace(a.Text)) >= 40` — skip terse
  acknowledgements so the check doesn't fire on "Done." / "Yes.").
- **`Evaluate`** (fast model): prompts with the reply and asks — *does this talk
  down to the user, use LLM/corporate jargon or shorthand (e.g. "delve",
  "leverage", "I'll help you with that!"), or over-hedge, instead of plain
  colleague-level English that assumes domain knowledge?* → `VIOLATION: yes` +
  one-line `CHALLENGE`, or `no`. Conservative default (fail-open) via the shared
  `parseVerdict`.

Added to the **default checks list** (`["debug-loop", "commit-checkpoint",
"plain-english"]`), so an enabled watchdog runs it. It fires one fast-model call
per non-trivial turn — cheap and local, and only when the watchdog is enabled
(default-OFF). Increment B' (watchdog settings) will let you toggle individual
checks.

## Error handling — never trap the turn

Fail-open is stricter here than for tool calls: a turn-end gate error, parse
failure, or model timeout **returns the reply unchanged**. The watchdog must
never wedge a conversation by refusing to let the model finish. (Same principle
as increment 1; the reopen path is only taken on an explicit `challenge`/`block`
verdict.)

## Data flow

```
model returns final text, no tool calls (toolloop.go:209)
  → WatchdogTurnEnd(ctx, finalText, hist)  [if set]
      → Gate(Action{Kind:"turn_end", Text, Transcript})
          → plain-english Applies? → Evaluate (fast model)
              allow  → return finalText
              challenge/block → append revise-instruction to history; continue loop
              escalate → PermissionRequester → allow: return / deny: reopen
  (error/nil anywhere → return finalText unchanged)
```

## Testing

- **Check** (`internal/watchdog`): stubbed `oneShot` — jargon reply → challenge;
  plain reply → clear; short reply → `Applies` false (skipped); nil oneShot →
  no violation.
- **Gate**: a `turn_end` action keys on the text (two different replies →
  different keys; same reply repeated → same key → escalates at threshold);
  `justify` on a turn_end challenge allows the same reply through.
- **RunToolLoop**: with a `WatchdogTurnEnd` stub returning `challenge`, the turn
  does NOT return the flagged text — the history gains the revise instruction and
  the loop runs another iteration (fake provider then returns clean text →
  allow → returns). With `allow`, the text returns immediately. Nil
  `WatchdogTurnEnd` → behavior identical to today.
- **Server**: the adapter maps `WatchdogTurnEnd` → `Gate(Action{Kind:"turn_end"})`.

## Out of scope (here)

- **Watchdog settings completeness** (increment B'): expose `mode` + per-check
  toggles (incl. this `plain-english`) in the settings page. Next increment.
- **Polish** (increment C): multi-conversation echo isolation, `parseVerdict`
  decorated affirmatives, dead test code.
- The matrix-router / fast-model class (separate subsystem; the "fast" model
  stays the co-processor lane).
