# Planning Mode Enforcement

## Problem / motivation

Planning mode is supposed to catch large, ambiguous, or multi-step requests and
route them through `suggest_plan` → the `y/n/d/c` approval gate → a read-only
planning profile → a human-signed spec and plan. In practice the model can
bypass the whole mechanism.

Observed failure (conversation `eacdedec3613124d`, turn 42060). The user said,
verbatim: *"i want a new tab in /config … we should put a plan together for this
work."* The model's own replies show it recognized this as planning work:

- "Let me confirm the building blocks exist **before writing a plan**."
- "This is planning work, so I'll write a spec + plan."
- "Let me write the effort spec and plan."

…and then it hand-authored `efforts/mcp-config-tab/spec.md` and `plan.md` with
ordinary file tools. It **never called `suggest_plan`**, so it skipped:

1. the `suggest_plan` capability and its X-tier confirm,
2. the `y/n/d/c` approval prompt,
3. entry into the read-only planning profile,
4. the sequential decision checkpoint.

Root cause is behavioral, not a wiring gap. The planning-mode trigger *is*
present in the always-on steering block (it is a `DomainCore` protocol), and the
gate works. But the trigger used the soft verb "propose," and the protocol body
assumed the model was *already* in planning mode — neither one forbade the model
from concluding "this needs a plan" and then writing the artifacts itself.

## Goals

- Concluding that a request needs a spec/plan MUST mean calling `suggest_plan`
  first — never hand-authoring `spec.md`/`plan.md` outside planning mode.
- The rule appears in both the terse steering trigger (always in the system
  prompt) and the full `planning-mode` protocol body (pulled via `get_protocol`).
- A regression test locks the wording so this can't silently drift back.

Non-goals:

- Path-based gating of writes to `efforts/**` (see Decisions — rejected).
- Any change to the gate, profile broker, or `suggest_plan` capability itself.

## Constraints

- Must reuse the existing steering/protocol substrate; the trigger is generated
  from the protocol so the two never drift.
- Must not reintroduce path-based tool gating, which was previously rejected in
  this project as a security anti-pattern.

## Decisions

We considered three ways to stop the model from hand-rolling a plan:

| Axis | A: Strengthen steering + protocol wording (prompt-only) | B: Hard gate on writes to `efforts/**/spec.md`/`plan.md` | C: Watchdog protocol supervision |
| --- | --- | --- | --- |
| Cost / complexity | Low — one trigger string, a body precondition, a guardrail bullet, one test | Medium — path matching in the tool gate + profile check | Medium-high — new watchdog rule + detection of "authoring a plan artifact while not in planning mode" |
| Risk | Low; purely additive to the prompt | Medium — misfires on legitimate mid-execution plan edits and non-effort specs; reintroduces a rejected pattern | Medium — depends on watchdog reliability, which is weak today |
| Reward / outcome | Directly addresses the model's stated reasoning; matches settled architecture | A true enforcement fence, but blunt | A principled, model-agnostic enforcement layer |
| Side effects | Still probabilistic — a "MUST" can be ignored | Couples the gate to file paths; brittle | Adds supervision load; only as good as the watchdog |
| Best reason | Cleanest fix, no new machinery, no rejected patterns | Guarantees compliance | Enforces regardless of prompt compliance |
| Main drawback | No hard guarantee | Path-based gating anti-pattern; edge cases | Watchdog isn't strong enough yet to lean on |

**Chosen: A (implemented now), with C as the next step once the watchdog is
stronger.** B is rejected: it is the path-based-gating hack we already ruled out,
and it breaks legitimate plan edits during execution. A is the cleanest fix that
fits the existing steering architecture; if it proves insufficient in practice, C
is the principled escalation — not B.

Arguing against A (the recommendation): A does not *guarantee* compliance the way
B would; a model can still ignore an imperative "MUST." We accept that trade-off
because the failure is the model choosing not to invoke a capability it was told
about — a steering problem — and because the watchdog (C) is the right hard
backstop, just not yet reliable enough to depend on.

## Next steps

- **C — watchdog supervision (deferred, next step):** once the watchdog is
  improved, add a rule that detects the model authoring `efforts/<slug>/spec.md`
  or `plan.md` while the read-only planning profile is *not* active, and
  challenges it to call `suggest_plan` instead. This is the hard backstop behind
  the prompt-only fix (A). Blocked on general watchdog reliability work.
