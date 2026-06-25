# Compaction 2b-1 — Live Wiring (Core) — Design

**Status:** Design approved. Implementation starting.

Wires the bake-off winner — **C, map-reduce with a model reduce pass** — into the
live agent so it actually sends a compacted history. Covers the production
summarizer, the **stateful frozen-segment** compactor, its persistence, the
trigger, and the request-path swap. Retention (2b-2) and `/c` integration +
explicit RPCs (2b-3) are separate follow-ons.

## Why

Today `server.go:1038` does `convHistory = agent.BuildLLMHistory(turns)` and
sends the **full** history every request. Real sessions reach hundreds of
thousands of tokens (corpus p99 = 632k, max = 1.05M), and the whole context is
re-sent every turn — slow and expensive. Compaction keeps the *sent* context
lean by replacing frozen older history with a consolidated summary, leaving
recent turns verbatim.

The bake-off (3 real sessions) settled the algorithm: **C wins** (tight,
deduplicated, coherent); **rolling is disqualified** (compounding loss collapses
its summary); B is C without the reduce pass. See
`compaction-bakeoff-findings.md`.

## Layering — agent-owned, client-agnostic

All of this lives in `source/server/internal/`. The request-path swap is internal
to the server; clients see a smaller context + the existing meter, unchanged.
2b-3 adds the client-facing RPCs (explicit trigger, read-original).

## 1. The frozen boundary (the anti-thrash core)

The trigger must fire on the **un-compacted tail**, never on total size —
otherwise a legitimately-large compacted context (its summary + recent verbatim
is just big) re-triggers every turn, re-summarizing already-summarized content
(rolling's death spiral). So we track a **frozen boundary**:

- **Frozen** turns: already compacted into per-segment summaries, **never
  touched again**.
- **Live tail**: raw turns after the boundary — the only thing the trigger
  measures.

Compaction advances the boundary by freezing new segments; it never re-summarizes
a frozen segment. The compacted context is *allowed to grow* organically (more
frozen summaries, kept consolidated by C's reduce); it simply stops
re-triggering because the tail empties.

## 2. Persistence — derived layer beside raw (SQLite)

Raw turns stay the source of truth (never destroyed by compaction). A new table
holds the derived state, 1:1 with a conversation:

```sql
CREATE TABLE IF NOT EXISTS conversation_compaction (
    conversation_id   TEXT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    frozen_through    INTEGER NOT NULL DEFAULT 0,  -- turns with created_at <= this are frozen
    segment_summaries TEXT    NOT NULL DEFAULT '', -- JSON []StructuredSummary, one per frozen segment
    consolidated      TEXT    NOT NULL DEFAULT '', -- JSON StructuredSummary, C's reduce over the segments
    compacted_tokens  INTEGER NOT NULL DEFAULT 0,  -- approx tokens the frozen segments replaced (for the meter)
    updated_at        INTEGER NOT NULL DEFAULT 0
);
```

(Migration follows the existing `ALTER TABLE … / duplicate-column` idempotent
pattern in `store.go`; the table is created in `schema.sql` for fresh DBs.) The
store gains `GetCompaction(convID)` / `SaveCompaction(...)` methods.

- **`frozen_through`** is the boundary (a `created_at`; turns at or before it are
  frozen). New turns always arrive after it, so they're live.
- **`segment_summaries`** are the frozen per-segment maps — reused, never
  recomputed. Appended to as new segments freeze.
- **`consolidated`** is C's reduce over all segment summaries — the preamble
  actually sent. Recomputed (one model call) only when a new segment freezes.

## 3. The production summarizer

A `SummarizeFunc` backed by the **local model directly** (mirrors recap's
`CompleteFunc`, *not* `StreamChat` — that was a bake-off harness device):

```
summarize(ctx, msgs) = ParseSummary( localProvider.Process(BuildSummaryPrompt(msgs)).Output )
```

Wired in `cmd/cercano/main.go` beside the recap generator, gated on a persistent
store existing. Runs off the request path.

## 4. The compaction pass (background, stateful)

Driven by a debounced generator (same shape as `recap.Generator`): after each
persisted turn, `Schedule(convID)`; a quiet period later, `runCompaction(convID)`:

1. Load turns + the stored compaction state.
2. **Gate — activation:** total tokens < activation floor → return (nothing to
   do; small contexts ride uncompacted).
3. Compute the **live tail** = turns after `frozen_through`. The
   **eligible-to-freeze** portion = the tail minus the recent verbatim window
   (last `VerbatimRecent` turns, kept live as the working set).
4. **Gate — cadence:** eligible-to-freeze < one segment (`SegmentTokens`) →
   return (let the tail keep accumulating).
5. **Elide** the eligible turns (mechanical tool-result dedup — corpus shows tool
   results are 56% of tokens, so this is most of the win and it's free).
6. **Segment** the elided eligible turns into `SegmentTokens` chunks; **map**
   each new segment through `summarize` (from raw, no compounding); append the
   results to `segment_summaries`; advance `frozen_through` past them.
7. **Reduce** (`MapReduceCompactor{ModelReduce:true}`-style model pass) over the
   full `segment_summaries` → `consolidated`. Save state.

Frozen segments from prior passes are reused as-is in step 7; only step 6's *new*
segments cost a per-segment model call.

**Sync override (hybrid):** in the request path, if `BuildLLMHistory(turns)`
would exceed the hard limit (≥90% of the model's max), run `runCompaction`
synchronously before sending, so a request never fails for lack of room.

## 5. Request-path swap (`server.go`)

Replace the single `BuildLLMHistory(turns)` with:

```
state := store.GetCompaction(convID)
if state has a consolidated summary {
    live := turns with created_at > state.frozen_through
    convHistory = AssembleSendView(state.consolidated, BuildLLMHistory(live))
} else {
    convHistory = BuildLLMHistory(turns)   // unchanged: nothing frozen yet
}
```

`AssembleSendView` (part 1) prepends the consolidated summary and runs
`RepairPairing`, so a tool_use frozen away whose result is still live is cleaned
automatically. The request path makes **no model calls** — the consolidated
summary is pre-computed by the background pass.

## 6. Data-driven defaults (from the corpus: 612 sessions, 95,686 turns)

| Knob | Default | Basis |
|---|---|---|
| Activation floor | 40k tokens | between session p75 (24k) and p90 (56k): skip the small ¾, compact the heavy tail |
| Segment / tail size | 8k tokens | median growth 368 tok/turn → freeze ~every 22 turns (~4 on busy sessions); fits a modest local context |
| VerbatimRecent | 6 turns | turns are tiny (median 57 tok) — a cheap recent working window |
| Hard override | 90% of model max | only the p99/max (632k–1M) sessions ever reach it |
| Elision | always-on | tool results are 56% of all tokens — free reclaim before any model call |

All configurable via the existing config system. Activation is **token-based,
not turn-count** — turns span 57 → 27k tokens, so a turn-count floor is
meaningless.

## 7. Error / edge

| Case | Behavior |
|---|---|
| Below activation floor | No compaction; full history sent (unchanged) |
| Summarize fails (local model) | Keep prior compaction state; retry next pass; never block a turn |
| Live result tool_use frozen, result live (or vice versa) | `RepairPairing` in `AssembleSendView` drops the orphan |
| Conversation has no compaction row | Treated as "nothing frozen"; full history |
| Sync override fires but summarize fails | Fall back to sending full history (request still succeeds, just large) |
| Turns deleted via `/c` below the boundary | Next pass recomputes from current turns; stale summary tolerated until then |

## 8. Testing

- **Compaction pass (deterministic, fake summarizer):** activation gate skips
  small contexts; cadence gate waits for a full segment; a pass freezes the
  eligible segments, advances `frozen_through`, reuses prior `segment_summaries`
  (asserts the fake is NOT re-called for already-frozen segments), and stores a
  consolidated summary. Verbatim window stays live.
- **Persistence:** `Save`/`GetCompaction` round-trip; migration adds the table to
  a legacy DB.
- **Request-path swap:** with a compaction row, the sent history is `consolidated
  preamble + live tail` and pairing-valid; without one, it's the full history
  (unchanged). No model call on the request path.
- **Sync override:** an over-hard-limit assembled history triggers a synchronous
  pass; a summarize failure there falls back to full history.

## Out of scope (follow-ons)

- **2b-2:** retention enforcement (raw 90d / compacted 180d / keep-forever / pin).
- **2b-3:** `/c` integration (show derived vs. original; the "compacted N · live M"
  metric) and the client-facing RPCs (explicit trigger, read-original).
- Hierarchical re-reduce when `segment_summaries` itself grows large (the reduce
  keeps it bounded for now).

## Key file references

| Concern | Location |
|---|---|
| Compactor C, summarizer, parser, assembly | `source/server/internal/compaction/` (parts 1–2) |
| Request-path swap | `source/server/internal/server/server.go:1038` |
| Store schema + migration + methods | `source/server/internal/conversation/{schema.sql,store.go}` |
| Background generator pattern | `source/server/internal/recap/recap.go` |
| Local summarizer wiring | `source/server/cmd/cercano/main.go` (beside recap) |
| Config thresholds | `source/server/pkg/config` |
| Token metering / model max | `source/server/internal/contextmeter` |
