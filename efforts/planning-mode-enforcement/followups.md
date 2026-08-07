# Planning-mode follow-ups

## FU-1: Agent can't tell it's already in planning mode; `suggest_plan` is blocked by its own profile — RESOLVED

**Status:** fixed. Both sub-defects addressed:

- **1a** — chose option (b) over (a): `suggest_plan` stays fenced (keeping it out
  of the catalog while planning is correct), but the fence's tool_result is now
  self-aware. `fenceDenialMessage` in `internal/agent/toolloop.go` special-cases
  `suggest_plan` under the `plan` profile and returns "already in planning mode —
  no need to call suggest_plan again … call request_plan_approval / plan_exit"
  instead of the contradictory "read-only … unavailable while planning" text.
  Option (a) was rejected because adding `suggest_plan` to `planExtraTools` would
  re-advertise it AND fire a spurious second X-tier confirm prompt for a no-op.
  Test: `TestFenceDenialMessage_SuggestPlanUnderPlanProfile_IsSelfAware`.
- **1b** — `BuildSystemPrompt` (`internal/runner/core.go`) now takes the active
  `agent.Profile` and, when it restricts, appends a `<planning-mode>` state
  signal telling the model it is already planning and what the exits are. The
  zero profile adds nothing (normal turns unchanged). Sub-agents pass the zero
  profile. Test: `TestBuildSystemPrompt_SignalsActiveProfile`.

Original report below for context.

---

## FU-1 (original report): Agent can't tell it's already in planning mode; `suggest_plan` is blocked by its own profile

**Observed** (transcript, mode: bypass, model qwen3-30b via claude cloud):

1. Agent narrates "I'm not in planning mode yet … Let me enter planning mode properly."
2. Calls `suggest_plan` → result: `⚠ blocked by plan profile`.
3. Agent then reasons its way out: "I'm already in the plan profile — suggest_plan
   is what puts you here, and I'm here. Good."

**Two distinct defects:**

### 1a. `suggest_plan` (and `request_plan_approval`?) blocked when already in plan profile
`planExtraTools` in `internal/agent/profile.go` whitelists the write hatch +
`request_plan_approval` + `plan_exit`, but NOT `suggest_plan`. So once the plan
profile is active, a second `suggest_plan` call is fenced with the self-
contradictory message "blocked by plan profile" — the very tool that enters the
mode is blocked *by* the mode.
- Decide: either (a) make a re-entrant `suggest_plan` a clean no-op that returns
  "already in planning mode" (add to planExtraTools + short-circuit in Execute),
  or (b) keep it fenced but return a clear, non-alarming message ("already in
  planning mode — no need to call suggest_plan again; proceed to author the spec").
  Prefer (a): re-entrancy should be graceful, not an error.

### 1b. Agent has no state signal that it's already in planning mode
The model narrated "I'm not in planning mode yet" while it *was* in the plan
profile. Nothing in its context tells it the active profile. It only recovered by
chance reasoning.
- Consider surfacing active-profile state into the turn context (system/steering
  block: "You are currently in planning mode (read-only fence active)."), so the
  model doesn't guess. This closes the loop with the planning-mode protocol text.

**Severity:** medium. Not data-loss, but produces confusing/self-contradictory
UX and relies on the model reasoning its way out of a wrong tool result.

**Repro:** enter plan mode via `suggest_plan`, then have the model call
`suggest_plan` again (common when it re-reads the protocol mid-plan).
