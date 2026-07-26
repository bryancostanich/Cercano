# Compaction Lifecycle — Budget, Erosion, and Session Rollover — Design

**Status:** Design proposed. Awaiting approval on D+C. A+B pre-approved and in progress.

This document covers four changes to the stateful frozen-segment compactor
(`source/server/internal/compactor`, `source/server/internal/compaction`). They
form one coherent story: **stop over-compacting, stop eroding detail, and give a
long session an honest end instead of an infinite squeeze.**

## Motivation — what's actually wrong

Observed live on conversation `58ce8a3d87ba1bc8` (1,778 turns, ~721k raw
tokens): the entire frozen history was being crushed toward a **~16k-token
ceiling**, and each time the merged ledger re-crossed that ceiling it was fed
back through the free-text summarizer — a paraphrase pass that `reduce.go`
itself already condemns as fabrication-prone. Two distinct defects:

1. **The ceiling is absolute and tiny.** `reconsolidateThresholdSegments = 2`
   (`compactor.go:65`) × `SegmentTokens = 8000` → a fixed **16 000-token** cap on
   the *entire* compacted backlog, regardless of the model's real context
   window. On a 200k-window model that spends ~8% of the window on the whole
   distilled history — a ~45:1 squeeze of this session. This is **fidelity
   starvation.**

2. **Re-consolidation paraphrases the ledger.** When the merged summary exceeds
   the cap, `Advance` (`compactor.go:246`) calls `summarize(...)` on the
   *already-structured* consolidated view and replaces all parts with the
   result. Because the input is already a structured summary, a second LLM pass
   can only paraphrase or invent — the same defect `reduce.go` removed from
   `Reduce`. Every re-cross degrades detail **monotonically**. This is the
   **erosion engine.**

Beyond both: even with a generous cap, **compaction has diminishing returns**.
No cap value makes an Nth-generation photocopy of a 1,778-turn session faithful.
Past some length the honest move is to **start fresh**, not compress harder.

## The four changes

| Change | Name | What it does | Status |
|---|---|---|---|
| A | Window-relative budget | Replace the fixed 16k cap with a config'd % of the active model's context window | Pre-approved |
| B | Kill the erosion engine | Delete the LLM-summarize-the-summary re-consolidation call; shrink deterministically | Pre-approved |
| C | Tiered retention | Recent segments stay high-detail, ancient ones compress hardest — the graceful "no" path | **Needs approval** |
| D | Agent-offered rollover | At a threshold, offer to cut a durable handoff and start a linked fresh session | **Needs approval** |

A and B are independent bug fixes and ship first. C and D are the lifecycle
redesign and are two halves of one flow, described below.

---

## A — Window-relative compaction budget (pre-approved)

Replace the fixed `reconsolidateThresholdSegments * SegmentTokens` bound with a
budget derived from the **active model's context window**, expressed as a
config'd fraction.

- New `Config` field, e.g. `CompactedBudgetFraction float64` (default ~0.30),
  and the budget is `fraction * activeContextWindow` computed at pass time from
  the resolver/context-meter, not a compile-time constant.
- Keep a sane floor so tiny-window local models still get a workable budget.
- `reconsolidateThresholdSegments` is retired.

Effect: on a 200k-window model the compacted backlog may occupy ~60k tokens
instead of 16k — enough that normal-length sessions never trip the shrink path
at all.

## B — Kill the erosion engine (pre-approved)

When the merged ledger *does* exceed budget, **do not** feed it back through the
free-text summarizer. Instead shrink the structured ledger **deterministically**
(the same philosophy `reduce.go` applied to `Reduce`):

- Cap `Decisions` / `Proposals` to the most-recent N (recency-ordered).
- Evict resolved `OpenThreads`.
- Coalesce `Files` (latest state per path already wins in `MergeSummaries`).

Load-bearing shapes (config YAML, signatures, tier lists) survive verbatim or
are dropped whole — never mangled into prose. The `IsEmpty` guard stays; a
deterministic prune can't summarize-to-nothing.

---

## C + D — the lifecycle flow (needs approval)

C and D are not alternatives. They are the **two branches of one offer.**

At a rollover threshold the agent **offers** a handoff:

- **User says "yes, start fresh"** → **D**: freeze the current consolidated
  summary into a durable handoff artifact, open a **linked new conversation**
  seeded only by that artifact, leave the old conversation fully intact on disk.
- **User says "no, keep going"** → **C**: we are now explicitly in
  user-chosen-long-session territory. Tiered retention keeps recent work
  high-detail and compresses ancient segments hardest — the honest way to keep
  compacting past the point where flat compaction is faithful.

D is always the *recommended* path; C is the graceful-degradation fallback that
makes declining non-catastrophic. Without C, a "no" drops straight back into the
infinite-compaction failure mode — which is exactly where flat re-consolidation
degrades worst.

### The offer must re-arm (hysteresis)

A one-shot offer means "no" once = "compact forever" again, a regression. The
trigger re-arms: after a decline at threshold T, it offers again at a higher
watermark (e.g. T × 1.5, or after K more re-consolidation cycles). C carries the
session *between* offers; D remains the recommended exit each time.

### Trigger signal

Lead signal: **cumulative raw tokens** of the conversation (the honest measure
of how much real history exists). Backstop: **re-consolidation count** (how many
times we've had to shrink) — a session that keeps hitting the shrink path is one
compaction can no longer serve well. Both are config'd; auto-roll is available
but **off by default** (rollover mid-thought is jarring; offered-by-default).

### D — the handoff artifact

The seed for the fresh session is **structured summary + a short verbatim tail**:

- The precursor's consolidated `StructuredSummary` (Goal / Decisions /
  Proposals / Files / OpenThreads / State) — the whole-session recap.
- **Plus the last N turns verbatim** (N = `VerbatimRecent`, ~6). Rollover most
  often fires mid-task; the structured summary carries *intent* (OpenThreads +
  State) but the verbatim tail carries the *immediate* thread the new session
  must pick up. Trivial one-time cost; does not compound.

### D — storage model: `precursor_id`

- New `conversations` row per rollover with a nullable
  **`precursor_id TEXT REFERENCES conversations(id)`**.
- `precursor_id` (not `parent_id`): the relationship is **temporal succession**,
  not containment. A rolled-over session isn't *inside* its predecessor; it
  *came after and inherited from* it. Chains read correctly N deep
  (A ← B ← C), walkable backward to reconstruct lineage for
  retrieval/debugging.
- The old conversation is **untouched** — turns, final compaction state, all of
  it stays cold and browsable. The baton is passed, not the history.
- The new conversation's **first turn is the handoff**; its frozen boundary
  starts at zero. No re-consolidation debt crosses the seam — within-session
  compaction only ever covers one session's worth, so the degrading
  summarize-the-summary loop is never needed inside a session.

Why `precursor_id` (new row) beats epoch-markers-in-one-conversation: with
markers, old turns physically remain in the same conversation and every query,
the context meter, and the compactor must special-case "ignore everything before
the last epoch marker." With a new row the boundary *is* the row — the live
session is just a session, small and honest; the old one is a different session
you can go read. No load-bearing marker turn, no special-casing.

### C — tiered retention shape

Segments carry an age/tier. Recent tiers retain full structured detail; older
tiers are pruned harder (the B-style deterministic prune, applied more
aggressively the older the tier). Newest segments never get summarize-the-summary
treatment. Exact tier boundaries to be specified in the C plan.

## Layering (hard constraint, unchanged)

All of this lives in the agent (server) layer. Clients touch it only through the
service interface. The rollover **offer** surfaces to the client as agent-driven
state (a prompt/affordance the CLI renders); the accept/decline is an RPC.
`precursor_id` is agent-side schema. No client reimplements any of it.

## Build / ship order

1. **A** — window-relative budget (worktree `fix/compaction-budget`).
2. **B** — delete the erosion engine, deterministic shrink (same worktree).
3. **D** — schema (`precursor_id`), handoff artifact, offer RPC + re-arm.
4. **C** — tiered retention as the decline path.

A+B are independently valuable and verifiable against the live 1,778-turn
conversation before D/C exist. D+C follow once this design is approved.

## Open questions for approval

1. Approve **D** (agent-offered rollover, `precursor_id` new-row model,
   summary + verbatim-tail handoff)?
2. Approve **C** (tiered retention as the decline fallback with a re-arming
   offer)?
3. Default for `CompactedBudgetFraction` (proposed **0.30**) and the rollover
   raw-token threshold (proposed to calibrate against the corpus, not guessed).
