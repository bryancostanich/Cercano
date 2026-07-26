# Planning Mode — Design

**Roadmap item:** #7 — Planning mode (Tier 2).
**Status:** Design (three core forks resolved; implementation not started).
**Depends on:** #3 task model (persistence + tree), the dispatch/subagent engine
(execution substrate), the permission substrate (gating + modes).
**Consumes:** Conductor's plan format; the existing `y/n/d/c` confirm gate.

Research backing this design lives in [`research/`](research/). The directional
findings (Claude Code's read-only Plan Mode, Conductor's spec-driven track format,
Superpowers' execution model) are credible and inform this doc; ignore any
fabricated quantitative metrics in the synthesis.

---

## 1. What planning mode is

Planning mode is a triad of activities, not a single monolithic state:

1. **Generate** — brainstorm and reason toward a solution shape (produces the spec).
2. **Capture** — lay the solution out as a phased, actionable plan (produces the plan).
3. **Execute** — walk the plan, do the work, and adapt when reality diverges.

Execution is a *separate* mode that may interleave with planning, not a phase of it.
The whole thing is glued together by one principle: **the plan is a living document,
never a frozen oracle.**

Three design forks decide the shape. All three are now resolved.

---

## 2. Fork 1 — What "read-only planning" means

**Resolved:** "read-only" means *exploration is non-mutating* — during generation, the
agent may read the world freely but may not change it. It does **not** mean the plan
itself is frozen or that the human is locked into a yes/no.

**Implementation:** reuse the permission substrate. A "plan" permission profile grants
only read-tier capabilities plus the `plan` capability. No new mode flag — planning
mode is a permission posture.

**Handoff to execution:** reuse the existing `y/n/d/c` confirm gate. The gate honors
the active permission mode (Strict / Permissive / Bypass). The four keys map onto the
planning handoff exactly as they already behave:

- **y** — approve the plan, begin execution.
- **n** — reject the plan (deny; with steering text, deny-with-feedback).
- **d** — show the plan (the plan is the "details" payload).
- **c** — "chat about this": drop into the composer to reshape the plan
  conversationally before it runs. This is the living-plan escape valve — you don't
  approve or reject, you *talk back* and the plan gets revised.

The `c` (chat/compose) path means the handoff needs **zero new UI**. Rejecting or
reshaping the plan at the gate is the same gesture the confirm prompt already supports.

---

## 3. Fork 2 — Where the plan lives, and in what format

**Resolved:** **Markdown files on disk are canon.** Human-readable, machine-parseable.
The recursive `Task` tree (from the #3 task model) is the *in-memory representation* —
what you get when you parse the files in, and what you serialize back out. The store is
a working form; **the file wins on disagreement.**

### 3.1 The effort — a directory with two docs

The unit of planned work is an **effort**. An effort is a directory holding two
canonical Markdown documents:

```
efforts/<effort-name>/
  spec.md      # what & why — human-owned, stable
  plan.md      # phased to-do — execution-owned, churns
```

- **`spec.md`** — the *what and why*: problem, goals, constraints, solution shape,
  non-goals. The durable reasoning artifact and the fixed point replanning anchors to.
  Changes rarely once settled.
- **`plan.md`** — the *how and in what order*: Conductor-style phases (each with an
  objective, files-to-touch, tests-to-write) containing task/sub-task checkboxes. The
  executable artifact — the thing execution walks and flips status on.

`<effort-name>` is slugged from the effort title. This mirrors Conductor's proven
layout (efforts-as-directories, Markdown docs inside).

### 3.2 The hierarchy

**Effort → (spec + plan) → Phase → Task → Sub-task.**

This maps onto the recursive `Task` node with no new node types:

- the effort is the root node,
- phases are its children,
- tasks are children of phases,
- sub-tasks are children of tasks.

Phase-level metadata (objective, files, tests) lives in the node's `Notes`.

`spec.md` is **not** parsed into the tree — it's prose, not tasks. It rides alongside
the root node as reference context: loaded, surfaced in the `d`/details view, but never
walked. This keeps the tree purely executable while the "why" travels with it.

### 3.3 Serialization (lossless round-trip)

`plan.md` maps to the tree by structure:

- `#` heading → effort title, followed by context prose.
- `##` heading → phase, followed by its objective/files/tests prose.
- `- [ ]` / `- [x]` / `- [~]` checkboxes → tasks and sub-tasks (nesting by indent).

Consequences:

- **Document order encodes sibling order.** No `order` field on the node is needed;
  the parser preserves sequence. (This closes the task-model "does sibling order
  matter" open question: it does, and file position encodes it natively.)
- **Heading depth + checkbox indent encode tree depth.**
- **Status lives in the checkbox glyph**, so mid-run execution writes are glyph flips
  in place — the human's prose sections are never disturbed.

Parse-then-serialize must reproduce the file (modulo intentional edits). The real
engineering this decision demands is that stable grammar and its round-trip. The known
hazard: the format must survive machine writes *and* human hand-edits landing between
them. Mitigated structurally by the spec/plan split (machine mostly writes `plan.md`;
human mostly owns `spec.md`).

### 3.4 Planning mode's internal two-step

Because spec and plan are separate artifacts, planning has an internal ordering
matching Conductor's Context → Spec → Plan → Implement and Cercano's generate →
capture → execute triad:

1. Generate the spec (`spec.md`); get sign-off.
2. Generate the plan (`plan.md`) from the approved spec.

The spec is the richest output of "generate"; the plan is "capture."

---

## 4. Fork 3 — The execution feedback loop

**Resolved:** execution responds to divergence in **three graduated tiers**, and the
active permission mode sets how aggressively execution self-patches vs. escalates.

A plan is a prediction, and predictions are wrong. Treating every surprise the same
way fails in both directions: reopen-everything causes replanning paralysis;
touch-nothing barrels through a plan known to be wrong. The response graduates to the
severity of the divergence.

### 4.1 The three tiers

**Local surprise → execution edits the plan in place, no handoff.**
A task is bigger/smaller/different than written, but the phase and goal hold. Execution
already writes `plan.md` (flipping status); adding, splitting, or annotating a task
within the current phase is the same kind of write. Done autonomously and recorded.
This is the common case and must be frictionless. Honors "no plan survives contact" by
default.

**Structural surprise → execution pauses and hands back to planning.**
A whole phase is invalidated (wrong approach, missed dependency, broken ordering) but
the spec still holds. Execution stops walking and re-invokes the plan-generation
capability with three inputs: the fixed `spec.md`, the current `plan.md` state (done /
not done), and the divergence that triggered the reopen. Planning reshapes the
remaining phases *against the spec*, then the revised plan re-enters through the **same
`y/n/d/c` gate**. Execution resumes from where it paused. No new mode, no new UI.

**Foundational surprise → execution stops and escalates to the human, spec included.**
Reality contradicts the spec itself. The machine must **not** silently rewrite the
spec — it is the human-owned anchor. Execution halts and surfaces the contradiction
("the spec assumed X; reality is Y; I can't proceed without a decision"). The human
edits `spec.md` (or abandons the effort); if the spec changes, the plan is regenerated
from the new spec. Rare, and deliberately heavyweight.

### 4.2 Who decides the tier — mode-tied

The executing model classifies each divergence, and the **active permission mode sets
the threshold**:

- **Bypass** — execution self-patches more aggressively; more divergence stays "local."
- **Permissive** — balanced.
- **Strict** — more divergence is pushed up to the gate; execution interrupts sooner.

This folds the feedback loop's aggressiveness into the permission dial the user already
understands, rather than introducing a separate setting. It also matches how the modes
already lean (Strict interrupts more).

### 4.3 Why this shape holds together

- **It reuses everything.** In-place edits reuse execution's write permission. The
  structural handoff reuses the planning capability and the `y/n/d/c` gate. The tier
  threshold reuses the permission mode. Only the *classification* of a divergence is
  new logic — a judgment the executing model makes and states, not new infrastructure.
- **The spec/plan split does real work.** Structural replanning has a fixed point to
  replan against; foundational surprise has a clear "don't touch without a human"
  boundary. Different change cadences (stable spec, churning plan) de-risk concurrent
  human/machine edits.
- **Markdown-canon files are the shared state across the loop.** Execution writes
  `plan.md`; planning re-reads and rewrites `plan.md`; the human reads both. Because
  canon is the file, the execution↔planning handoff is just both sides operating on the
  same file — no state serialized across a mode boundary, no in-memory tree handed off.
  Each side re-parses from the file. This is the payoff of the Fork 2 decision.

---

## 5. Vocabulary (locked)

| Term | Meaning |
|------|---------|
| **effort** | A named body of planned work; a directory under `efforts/`. |
| **spec** (`spec.md`) | The what & why. Human-owned, stable, the replanning anchor. |
| **plan** (`plan.md`) | The phased to-do. Execution-owned, churns. Parses to the task tree. |
| **phase** | A titled section of the plan with an objective, files, and tests. |
| **task / sub-task** | Checkbox items within a phase; nodes in the recursive `Task` tree. |

Hierarchy: **Effort → (spec + plan) → Phase → Task → Sub-task.**

---

## 6. Open sub-questions (non-blocking)

- The exact Markdown grammar for phase metadata (objective / files / tests) — inline
  prose vs. a light structured convention. Resolve in implementation.
- How "promote to plan" reads in real time during a generation/execution session (UX
  detail, not architecture).
- Whether foundational-surprise escalation should offer the human an inline spec-edit
  affordance or just drop to the editor. Resolve in implementation.

---

## 7. Future enhancements (out of scope for first pass)

### 7.1 Browser-served brainstorming artifacts

Superpowers' brainstorm step doesn't stop at prose in the terminal — during the
**generate** activity it spins up a local browser view and *serves* interactive UX
mockups and idea explorations as HTML, so brainstorming output is a visual, browsable
artifact the human can react to rather than a wall of text. (The
`.superpowers/brainstorm/*.html` scratch files we found are exactly these served
artifacts.)

This is a genuinely nice affordance for the **generate** step and worth adopting —
visual UX ideas land far better in a browser than in a terminal. **Explicitly out of
scope for the first pass.** First pass ships planning mode with terminal/Markdown
artifacts only (spec + plan). A later iteration can add a local served-HTML brainstorm
surface as an optional generate-step output, written into the effort directory (e.g.
`efforts/<name>/brainstorm/*.html`) so it lives with the rest of the effort's
artifacts and is versioned alongside spec and plan.

Deferred, not rejected — captured here so it isn't lost.
