---
name: design-decisions
description: Stop and weigh real options before coding a structural decision.
---

# Design Decision Protocol

## When This Applies

Any time there are multiple viable implementation approaches — data modeling, encoding choices, interface changes, module boundaries, state machine structure, anything structural. If you can think of more than one way to do it, this protocol is mandatory.

## The Rule

**STOP. Do not write code.** Present options to the human first.

## Steps

1. **Identify the decision point.** State what needs to be decided and why.

2. **Enumerate at least 2–3 real options.** Not strawmen. Each must be a genuinely viable approach.

3. **For each option, quantify these four dimensions:**

   - **Cost/complexity**: lines of code, files touched, new dependencies introduced, test surface added. Be specific — "~50 new lines across 3 files" not "small." (Hardware analog: gates, registers, mm².)
   - **Risk**: what can go wrong? Is the failure silent (wrong output) or loud (crash/hang)? What testing catches it? How hard is it to debug if it breaks?
   - **Reward/outcome**: what does this solve? Does it unlock future work? Does it close off future options?
   - **Side effects**: performance implications, build/test overhead, does it simplify or complicate other subsystems? Check for secondary/emergent benefits — the best option often wins on multiple axes simultaneously (e.g., solving the primary problem AND fixing a latency issue AND simplifying a future phase).

   **Symmetric quantification rule**: every dimension or concern you raise for *one* option must be evaluated for *every* option on that dimension, even if the answer is "same" or "n/a." Asymmetric framing — tagging "stability concerns" on Option C without checking whether Option B has the same concern, or calling B "simpler" without counting B's actual moving parts — is where confirmation bias hides. If a concern applies to multiple options, that concern is not a differentiator and shouldn't be presented as one.

4. **Explicitly flag hacks.** If an option conflates unrelated concerns, overloads a field for a dual purpose, or works "because there happen to be unused slots," call it a hack. Do not dress it up.

5. **Argue against your own recommendation.** Before locking a recommendation in step 6, write down the strongest case *for each non-recommended option*. If you can't make a substantive case for the alternatives, your analysis is thin — go back to step 3 and look for what you missed. If the counter-cases are genuinely weak after honest effort, the recommendation is sound. This step exists because the protocol relies on honest enumeration in step 3, and confirmation bias can quietly stack the framing toward a preferred option without anyone noticing until the wrong choice ships.

6. **Recommend the cleanest option**, even if it's more work. Bias toward semantic correctness and clean architecture over implementation convenience.

7. **Wait for human approval** before writing any code.

## What Counts as a Hack

- Packing unrelated concerns into the same data structure because there are unused fields (hardware analog: spare register bits)
- Reusing a field or variable for a different purpose in a different context
- Encoding that requires the reader to know "this field also secretly carries X"
- Any approach you'd pick because it's fewer lines changed, not because it's the right design

## Why This Exists

The lazy option accumulates tech debt. A quick hack today becomes a "why does this function do two unrelated things?" mystery in 6 months. Clean architecture costs more up front but pays back every time someone reads the code — including future you.

The best architectural decision often wins on multiple axes simultaneously. Always look for the option that solves the most problems at once, even if it costs more implementation effort.

