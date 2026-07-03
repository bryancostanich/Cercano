---
name: systematic-debugging
description: Reduce, observe, reason, predict, probe, then fix — no guessing.
---

# Systematic Debugging Protocol

You are now operating under Dave's debugging protocol. This is not optional guidance — it is the mandatory sequence for investigating any failure. Agents who skip steps reliably produce wrong fixes that waste time and create new bugs.

## The Protocol

Every debug cycle follows this sequence. No skipping steps.

### 1. REDUCE

Before tracing anything, reduce to the **smallest case that still fails**.

- Remove variables one at a time until you find the one that matters
- If a complex test fails, try the simplest version first
- If multiple things are broken, isolate one failure before investigating
- The dimension you add back that triggers the failure is where the bug lives

If the minimal case passes, use **divergence-point analysis**: compare the passing case against the failing case signal-by-signal, log-by-log, output-by-output. The first thing that diverges is closest to root cause.

### 2. OBSERVE

What does the data show? Not what you think should happen — what **actually** happens.

- List specific values, states, outputs at specific points
- Cover ALL relevant signals/variables, not just the one that looks wrong
- Use real data: logs, debugger output, test results, actual error messages
- Do NOT use your mental model as evidence. Your mental model is a hypothesis.
- If comparing two cases, lay them side by side

### 3. REASON

Trace the logic step by step to a **specific mechanism** that explains ALL observed data.

- Follow every variable, every branch, every state transition
- Run the reasoning to its conclusion — not "it might be X" but "the specific mechanism is X because of data points A, B, C"
- If your theory explains some observations but not others, the theory is wrong
- If you can't explain all the data, you haven't reasoned far enough

### 4. PREDICT

State specific, falsifiable predictions:

```
"If [hypothesis], then [specific observable] at [specific point] should be [specific value]"
"If [hypothesis is wrong], then [specific observable] will be [other value]"
```

A prediction you can't check is useless. Every prediction must name something you can actually observe.

### 5. PROBE

Check the prediction against actual data. **This step is mandatory before any fix.**

- Run the test, check the log, inspect the variable, read the output
- Compare actual vs. predicted
- If the probe **confirms** the prediction: proceed to FIX
- If the probe **contradicts** the prediction: the hypothesis is wrong. **Go back to OBSERVE.** Do not patch the hypothesis — start over with the new data.

### 6. FIX

Only after root cause is confirmed by PROBE:

- Apply ONE change that addresses the root cause
- If it doesn't work, **revert before trying the next thing**
- After fixing, verify the original failure is resolved AND no new failures were introduced

## Rules

- **Never apply a fix before confirming root cause with data** (PROBE complete)
- **Never stack multiple changes** — one hypothesis, one run
- **Never theorize without data for more than 2 minutes** — check actual state
- **Never skip PROBE** — a fix based on REASON alone is a guess, no matter how confident you are
- **The output is TRUTH.** Your mental model is a hypothesis.
- **If you catch yourself saying "it should be..."** — stop. Check what it actually is.

## Anti-Patterns

| What goes wrong | What to do instead |
|---|---|
| "Let me just try changing this" | OBSERVE first. What does the data say? |
| Changing 3 things at once | One change. Run. Check. |
| "It must be X" without checking | That's REASON without PROBE. Check. |
| Guessing for 20 minutes | 2 minutes of theory max, then look at actual data |
| Fix didn't work, trying another fix | Revert first. Then re-OBSERVE with new data. |
| "That's weird, let me try..." | Weird = your model is wrong. OBSERVE harder. |

