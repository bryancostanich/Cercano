package protocols

var builtinProtocols = []Protocol{
	{
		Name:        "design-decisions",
		Description: "Stop and weigh real options before coding a structural decision.",
		Domain:      DomainCore,
		Trigger:     "Facing a real decision with more than one viable approach → stop, enumerate the real options with their trade-offs in plain English, and get human approval before writing code.",
		Body: `# Design Decision Protocol

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

   **Required rollup table**: after identifying the options, present the comparison as a terminal-safe Markdown matrix with **decision axes as rows** and **short option labels as columns**. Do not use a wide row-per-option table that crams cost/risk/reward/side effects into one row. Do not put long option names in the table header, and do not put sentences in cells.

   Before the table, define a short legend using terse labels:

   - **A — Disable by default**: fresh installs leave watchdog off.
   - **B — Disable one check**: keep watchdog on, remove noisy check.
   - **C — Tune checks now**: keep watchdog on, fix noisy checks.

   Then use this compact shape:

   | Axis | A | B | C |
   |---|---|---|---|
   | Cost | Low | Low | Med |
   | Risk | Low | Med | Med |
   | Reward | Stops surprises | Keeps some coverage | Preserves feature |
   | Side effects | Opt-in only | Still noisy | More work now |
   | Best reason | Safest default | Narrow change | Fixes root causes |
   | Main drawback | Less coverage | Partial fix | May still miss cases |

   Table rules: keep headers short, keep each cell under ~6 words, and keep the whole table readable in an 80–100 column terminal. Put nuance below the table in prose bullets. If the table would wrap, shorten the cells — do not emit an unreadable table.

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
`,
	},
	{
		Name:        "systematic-debugging",
		Description: "Reduce, observe, reason, predict, probe, then fix — no guessing.",
		Domain:      DomainCore,
		Trigger:     "Before applying any fix to a bug or test failure → reduce to the smallest failing case, observe the actual data, and confirm the root cause with a probe; never fix on reasoning alone.",
		Body: `# Systematic Debugging Protocol

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

` + "```" + `
"If [hypothesis], then [specific observable] at [specific point] should be [specific value]"
"If [hypothesis is wrong], then [specific observable] will be [other value]"
` + "```" + `

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
`,
	},
	{
		Name:        "verification-strategy",
		Description: "Match the test tier to the size of the change.",
		Domain:      DomainCore,
		Trigger:     "Match the test tier to the change — don't run the full end-to-end suite for an internal change, and don't skip integration tests when an interface changed.",
		Body: `# Verification Strategy Protocol

You are now operating under Dave's verification protocol. Before running tests, think about WHICH tests to run. Running the full suite when you only changed one function wastes time. Skipping integration tests when you changed an interface misses bugs.

## Verification Tiers

### Tier 0: Unit / Isolated

Test the component in isolation. Mock or stub its dependencies. Fastest.

**Use when:** You're changing internal logic that doesn't cross boundaries. Algorithm changes, data transformations, pure functions, state machine logic.

**Don't use when:** You've touched an interface, changed data formats, or modified how components communicate.

### Tier 1: Integration / Smoke

Test the component in context with its immediate neighbors. Real dependencies, controlled inputs.

**Use when:** You've changed an interface, modified data flow between components, or touched boundary code. Also use as a sanity check after significant Tier 0 changes.

**Don't use when:** You need to validate end-to-end behavior at real operating conditions.

### Tier 2: System / End-to-End

Full system test at real operating conditions. Slowest. This is the sign-off test.

**Use when:** Pre-merge validation, release sign-off, or investigating bugs that only appear under realistic conditions.

**Don't use when:** You're iterating on a single component — use Tier 0 or 1 and save Tier 2 for when you're ready to validate.

## Rules

- **Match the tier to the change.** Don't run Tier 2 when Tier 0 covers what you changed.
- **Don't skip Tier 1 when you've touched an interface.** Unit tests won't catch integration bugs.
- **Run only affected tests during iteration.** Full suite for sign-off, targeted tests for development.
- **If a Tier 0 test passes but Tier 1 fails**, the bug is at the boundary — focus there.
- **If Tier 1 passes but Tier 2 fails**, the bug is in system-level interactions — timing, ordering, resource contention.
`,
	},
	{
		Name:        "compute-before-simulate",
		Description: "Compute the expected result before running any simulation or sweep.",
		Domain:      DomainCore,
		Trigger:     "Before running a simulation, benchmark, or parameter sweep → compute the expected result analytically first; the run verifies the math, it doesn't replace it.",
		Body: `# Compute Before Simulate

You are now operating under Dave's simulation protocol. The simulation verifies your computation — it does not replace it.

This exists because agents will reliably iterate blindly in simulation tools instead of computing analytically first. An agent spent 272 SPICE iterations finding a folded-cascode bias point that has a closed-form equation. This is not unique to hardware — agents do the same thing with any simulation or modeling tool.

## The Protocol

### 1. Identify What You're Determining

State the specific quantity or behavior you're trying to find. Not "see if it works" — what specific value or property?

### 2. Find the Governing Equations

The equations exist. For well-understood systems, the analytical solution is known. Look it up.

- Circuit design: Razavi, Allen & Holberg, Gray & Meyer
- Structural: beam equations, stress formulas
- Thermal: heat transfer coefficients, thermal resistance
- Performance: Little's law, queueing theory, Amdahl's law
- ML: convergence bounds, learning rate schedules

If you're working in a domain where closed-form solutions exist and you're not using them, you're brute-forcing.

### 3. Compute the Expected Result

With actual parameters, not placeholders. Write down:
- The equation you're using
- The values you're plugging in
- The expected result

### 4. Run ONE Simulation

One. To verify your computation.

### 5. Evaluate

- **Within ~20% of prediction**: Fine-tune with 1-3 more runs. You understood the system.
- **Wildly off**: The equations are wrong, or you used wrong parameters. Go back to step 2. Do NOT iterate in the simulator to "find" the right answer.

### 6. Hard Limits

- Maximum 3-5 simulation runs for initial design verification
- If you're past 5 runs, STOP. You're brute-forcing.
- Parameter sweeps are for characterizing a FINISHED design, not for finding the design

## What This Prevents

- Parameter sweeps to "find" a value that has a closed-form equation
- "Let me just try X and see what happens" — that's guessing, not engineering
- Iterating 50+ times when 3 runs should suffice
- Using simulation as a design tool instead of a verification tool
- Skipping the math because "the simulator will figure it out"

## The Test

Before running any simulation, you should be able to answer:
1. What specific result do I expect?
2. What equation produced that expectation?
3. If the simulation disagrees, which do I trust and why?

If you can't answer these, you don't understand the system well enough to simulate it.
`,
	},
	{
		Name:        "worktree-first",
		Description: "Use a separate worktree for substantial work; keep tiny, explicit current-branch edits lightweight and safe.",
		Domain:      DomainCore,
		Trigger:     "Before creating a new feature branch → use the `git_worktree` tool for substantial work; for tiny current-branch edits, ask when unsure and checkpoint only explicit paths.",
		Body: `# Worktree-First Protocol

## When This Applies

Any time you're about to decide where a change should happen.

Use a separate worktree for substantial work: new features, risky fixes,
logic changes, multi-file edits, or anything likely to take more than a
quick pass.

For tiny, explicit edits — a typo, label rename, comment fix, or one-line
copy tweak — it is okay to stay on the current branch when that is clearly
what the user wants. If it is not clear, ask: "Do you want this done here
or in a separate workspace?"

## The Rule

**Match the workspace to the risk.** The root worktree (the top-level
repository directory) is a shared physical checkout — multiple concurrent
sessions read from it and write to it. For substantial work, create an
isolated worktree. For tiny current-branch work, do not scoop up unrelated
local changes: checkpoint only the explicit paths you touched.

## Steps

1. **Use the ` + "`git_worktree`" + ` tool.** Not raw ` + "`git checkout -b`" + `, not
   ` + "`git worktree add`" + ` from the shell — the tool wraps both and adds the
   safety checks (clean baseline, target dir git-ignored, trunk
   resolution). Pass:
   - ` + "`path`" + `: ` + "`../<repo-name>-<feature-slug>`" + ` — a **sibling**
     directory to the repo root, not a subdirectory of it. Sibling paths
     avoid gitlink-submodule confusion (git treats a linked-worktree
     directory *inside* the tracked tree as a submodule pointer, which
     creates persistent ` + "`M`" + ` entries in ` + "`git status`" + ` on
     the root as the worktree's HEAD advances). Example: for a repo at
     ` + "`/git_repos/foo/Cercano`" + `, a good worktree path is
     ` + "`/git_repos/foo/Cercano-runtime-dashboard`" + `.
   - ` + "`branch`" + `: ` + "`feat/<feature-slug>`" + ` or ` + "`fix/<feature-slug>`" + `.
   - ` + "`trunk`" + `: the target trunk (usually ` + "`main`" + `).

2. **Do all work inside the worktree.** Every ` + "`cd`" + `, test run, edit, and
   commit lives in the isolated directory. The root stays on trunk,
   untouched.

3. **Do not ` + "`git checkout <branch>`" + ` in the root worktree** while your
   feature is active. If you need to look at trunk state, do it in the
   root (which is already on trunk). If you need to look at your branch,
   do it in the worktree.

## Why This Exists

The root worktree is the default directory other sessions operate in
when they touch the repo. If your feature branch is checked out there:

- Other agents committing "quick fixes" for unrelated tracks land on
  your branch instead of trunk.
- Any nested worktree left inside the tracked tree gets recorded as a
  submodule pointer whose HEAD keeps drifting — showing up as a
  persistent conflict every time you rebase. The sibling convention
  above sidesteps this entirely.
- Rebasing back onto trunk multiplies conflicts by every stowaway
  commit — each one carries its own worktree-pointer noise that has to
  be reconciled per commit.
- Rolling back is dangerous — other sessions may still be reading the
  checked-out working tree.

A worktree is one tool call to create with ` + "`git_worktree`" + `. It isolates
all of this at zero ongoing cost. Skipping the worktree to save one step
now trades minutes for hours of rebase conflict resolution later.

## What This Prevents

- Concurrent-modification hazards between agent sessions sharing the root
- Submodule-pointer conflicts on nested-worktree paths you never touched
  (avoided entirely by the sibling-directory convention above)
- Unrelated commits from other sessions accumulating on your feature
  branch
- "How did I end up with 14 commits I don't recognize?" investigations
  mid-rebase

## Fast Path for Tiny Current-Branch Changes

For genuinely tiny changes — a typo, a label rename, a comment fix, or a
one-line copy tweak — the worktree ceremony is disproportionate. The fast
path is:

- Applies to small edits with no logic changes.
- Stay on the current branch when the user clearly wants a quick edit here.
- If it is not clear, ask whether to work here or in a separate workspace.
- Checkpoint with explicit ` + "`paths`" + ` and, when the current branch is trunk,
  explicit ` + "`allow_trunk`" + `. This stages only the files you name and leaves
  unrelated local work alone.
- Never use raw ` + "`git add -A`" + ` for current-branch quick edits.

The fast path is deliberately narrow. Anything with a logic change, a new
file, or touching more than a couple of files goes through the full
worktree flow. When in doubt, ask the user which workspace they want.
`,
	},
	{
		Name:        "delegate-git-plumbing",
		Description: "Delegate noisy git plumbing to a sub-agent so its churn stays out of the main context.",
		Domain:      DomainCore,
		Trigger:     "Before running git plumbing (worktree, rebase, status/diff, fast-forward, land, bisect) inline → delegate it to a sub-agent via the `dispatch` tool (aka `workflow`), which returns just the branch and SHA so porcelain and diffs stay out of the main context.",
		Body: `# Delegate Git Plumbing Protocol

Git plumbing is noisy and its output has no lasting value once the operation
is done — only the outcome (which branch, which SHA, or which file
conflicted) matters afterward. Run it in a sub-agent, not inline, so the
porcelain, diffs, and rebase or merge logs never flood the main context.

## The Rule

When the work is git mechanics — creating a worktree, rebasing, inspecting
status or diff, fast-forwarding, running a land's test-gate, or bisecting —
hand it to a sub-agent via the ` + "`dispatch`" + ` capability (its ` + "`workflow`" + ` alias
works too). The sub-agent does the churn and returns one line: the resulting
branch and short SHA, or "conflict at <file>".

## Guardrails

- The sub-agent must scope every command with git -C <abs-worktree> and
  report the exact branch and SHA it acted on. A sub-agent's git can
  otherwise silently land in the shared main checkout.
- Merge-to-main is never implied by delegation. Only land to main when the
  user's instruction for that specific work authorized it.
- Authorization and final verification stay with the top-level agent. Only
  the mechanics are delegated — you still confirm the result yourself.
- Read-only git delegations run silent; granting write or exec tools trips a
  single confirm-once prompt at dispatch time. That is itself a reason to
  bundle a whole git operation into one sub-agent call rather than many.

## What This Prevents

- Rebase logs, status dumps, and diffs flooding the shared context for work
  whose only durable output is a ref.
- Sub-agent commits landing on the wrong branch or the shared main checkout.
- A delegation being mistaken for standing permission to merge to main.
`,
	},
}
