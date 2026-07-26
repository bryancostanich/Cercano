# Compaction Lifecycle — Budget, Erosion, and Session Rollover — Design

**Status:** Implemented on `fix/compaction-budget` (A+B+C+D). See "As-built" at
the end for what shipped, field names, and one carried caveat.

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
| A | Window-relative budget | Replace the fixed 16k cap with a config'd % of the active model's context window | **Shipped** |
| B | Kill the erosion engine | Delete the LLM-summarize-the-summary re-consolidation call; shrink deterministically | **Shipped** |
| C | Tiered retention | Recent segments stay high-detail, ancient ones compress hardest — the graceful "no" path | **Shipped** |
| D | Agent-offered rollover | At a threshold, offer to cut a durable handoff and start a linked fresh session | **Shipped** |

A and B are independent bug fixes and shipped first. C and D are the lifecycle
redesign and are two halves of one flow, described below.

---

## A — Window-relative compaction budget

Replace the fixed `reconsolidateThresholdSegments * SegmentTokens` bound with a
budget derived from the **active model's context window**, expressed as a
config'd fraction.

- Config field `CompactionConfig.CompactedBudgetPct float64` (YAML
  `compacted_budget_pct`, default **0.30**). The budget in tokens
  (`compactor.Config.CompactedBudgetTokens`) is computed **once at construction
  time** in `main.go` as `contextmeter.ModelMax(cfg.OpenChatModel()) *
  CompactedBudgetPct`, not at pass time — the compactor is only built in
  `main.go`, so no proto/worker wire plumbing was needed.
- A floor of **16 000 tokens** (`compactedBudgetFloorTokens`) keeps tiny-window
  local models workable; a zero budget falls back to the legacy segment bound.
- `reconsolidateThresholdSegments` is retired.

Effect: on a 200k-window model the compacted backlog may occupy ~60k tokens
instead of 16k — enough that normal-length sessions never trip the shrink path
at all.

## B — Kill the erosion engine

When the merged ledger *does* exceed budget, **do not** feed it back through the
free-text summarizer. Instead shrink the structured ledger **deterministically**
via `pruneToFit` (the same philosophy `reduce.go` applied to `Reduce`) — it
removes whole entries in recency order (oldest first) across OpenThreads,
Proposals, Decisions, Files until under budget:

- Drop entries whole, recency-ordered (oldest first).
- Goal and State are preserved verbatim and never pruned.
- Coalesce `Files` (latest state per path already wins in `MergeSummaries`).

Load-bearing shapes (config YAML, signatures, tier lists) survive verbatim or
are dropped whole — never mangled into prose. The summarizer is **never** called
on an already-consolidated summary. The `IsEmpty` guard stays; a deterministic
prune can't summarize-to-nothing.

---

## C + D — the lifecycle flow

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

## As-built

All four changes shipped on `fix/compaction-budget`. Config, field names, and
the file map as actually implemented:

**Config (`CompactionConfig`, YAML `compaction:`)**

| Field | YAML key | Default | Meaning |
|---|---|---|---|
| `CompactedBudgetPct` | `compacted_budget_pct` | `0.30` | Fraction of the chat model's window for the compacted backlog (floored at 16k tokens). |
| `TieredRetentionSegments` | `tiered_retention_segments` | `0` (off) | Count of newest segments kept verbatim; older tiers pruned harder. |
| `RolloverRawTokenThreshold` | `rollover_raw_token_threshold` | `0` (off) | Raw-token watermark that arms the rollover offer. |
| `RolloverReconsolidationThreshold` | `rollover_reconsolidation_threshold` | `0` (off) | Reconsolidation-count backstop trigger. **See caveat.** |
| `RolloverRearmMultiple` | `rollover_rearm_multiple` | `1.5` | After a decline at T, re-arm at T × this. |
| `RolloverVerbatimTurns` | `rollover_verbatim_turns` | `6` | Verbatim tail length in the handoff artifact. |

Rollover is fully off unless a threshold is non-zero. Budget/pruning are always on.

**File map**

- Budget + deterministic shrink: `compactor/compactor.go` (`budgetTokens`,
  `pruneToFit`), wired in `cmd/cercano/main.go` (`compactedBudgetDefaultPct`,
  `compactedBudgetFloorTokens`).
- Tiered retention: `compactor/compactor.go` (`applyTieredRetention`, ahead of
  `Reduce`; `pruneToFit` remains the final backstop).
- Storage: `conversation/schema.sql` + `store.go` (`precursor_id`,
  `CreateRolledOver`, `Precursor`).
- Offer state machine + handoff: `server/rollover.go` (`rolloverManager`,
  `buildHandoff`); emitted in `server.go` (`maybeOfferRollover`) at the turn
  boundary before `FinalResponse`.
- Contract: `proto/agent.proto` (`RolloverOffered`, `AcceptRollover`,
  `DeclineRollover`).
- CLI: `clients/cli/internal/ui/` (offer prompt via the generic confirm gate;
  accept switches into the new session with a scrollback seam) and
  `pkg/agentclient/client.go` (`TypeRolloverOffered`, `AcceptRollover`,
  `DeclineRollover`).

**Caveat — reconsolidation-count trigger is inert.** The offer currently gates
on **raw tokens only**. Nothing in the pipeline persists a reconsolidation
counter yet, so the OR-branch on `RolloverReconsolidationThreshold` always sees
0 and never fires (there is a `// TODO` at the source). Making that trigger live
requires the compactor to persist a shrink counter first — a small, separate
follow-up. The raw-token trigger is fully functional.

**Follow-up.** Calibrate the default rollover raw-token threshold against the
corpus rather than guessing; it ships at `0` (off) until then.
