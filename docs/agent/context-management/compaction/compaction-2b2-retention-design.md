# Compaction 2b-2 — Retention Enforcement — Design

**Status:** Design approved. Implementation starting.

The last functional piece of compaction. The engine keeps raw turns forever today;
2b-2 bounds that with a layer-aware, age-based retention sweep: stub aged raw
bodies, collapse long-dead conversations to an identity stub, with a
keep-forever escape hatch.

## Why

Raw turns (mostly tool-result bodies — ~90% of context bytes, ~1–5 GB/year for a
heavy user) are kept forever by the compaction engine; the summary makes them
redundant once frozen. Without retention the DB grows unbounded. 2b-2 reclaims
that space on a schedule while keeping the cheap compressed memory (summaries,
recap, title) long-term.

## Layering — agent-owned, server-side

All server-side: a background sweeper + two store operations + config. No client
work. (A future per-conversation pin or `/c` retention status would be the only
client touch — both deferred.)

## The sweeper

A `retention.Sweeper` mirroring the recap/compaction generator pattern:

- Runs ~30s after startup, then every **12h** (`time.Ticker`), off the request
  path. Wired in `cmd/cercano/main.go` beside the recap/compaction generators,
  gated on a persistent store existing.
- If `keep_forever` is set, the sweep is a no-op (aging disabled entirely).
- Failures are swallowed per conversation — one bad conversation never aborts the
  sweep, and the sweep never blocks a turn.

## The policy (per conversation, in order)

1. **180-day collapse.** If `last_turn_at` is older than
   `compacted_retention_days` (default 180): delete the
   `conversation_compaction` row AND all of the conversation's turns, leaving
   only the `conversations` row (its `title` + `recap`). The session stays in
   `-r` history as an identity stub; a full delete remains an explicit user
   action. (`CollapseConversation`.)
2. **90-day raw stub.** Otherwise, for **frozen** turns
   (`created_at <= frozen_through`) older than `raw_retention_days` (default 90):
   `UPDATE` their `content`/`content_json` to a placeholder
   (`"[pruned after 90 days — see summary]"`). Only frozen turns are touched —
   the summary already captured them; un-summarized recent/live raw is never
   pruned. (`PruneRawBodies`.)

A conversation that was never compacted (no `frozen_through` — small/recent) has
nothing frozen to prune, so the 90-day stage skips it. The 180-day stage still
collapses it if it's been dead that long (its turns + any data go, identity
stays).

Both operations are **idempotent**: re-stubbing an already-stubbed turn changes
nothing (guarded by a `created_at <= frozen_through AND created_at < cutoff`
predicate); collapsing an already-collapsed conversation deletes nothing.

## Config

A `Retention` block on `CompactionConfig` (`pkg/config`):

```yaml
compaction:
  retention:
    raw_retention_days: 90
    compacted_retention_days: 180
    keep_forever: false
```

Invariant the defaults respect: `compacted_retention_days >= raw_retention_days`
(the cheap summary outlives the expensive raw).

## Store operations

Two new `conversation.Store` methods, both under the store mutex:

- `PruneRawBodies(ctx, conversationID string, beforeUnix, frozenThrough int64) (pruned int, err error)`
  — `UPDATE turns SET content = '<stub>', content_json = '' WHERE conversation_id = ?
  AND created_at <= frozenThrough AND created_at < beforeUnix AND content != '<stub>'`.
  Returns the row count for logging.
- `CollapseConversation(ctx, conversationID string) error` — delete the
  `conversation_compaction` row and all `turns` for the conversation (the
  `conversations` row is untouched). Idempotent.

The sweeper enumerates conversations via the existing `List(ctx, "", 0)` (which
returns `Info` with `LastTurnAt`) and reads `frozen_through` via `GetCompaction`.

## Interaction with the live engine

- Stubbing frozen turns does **not** change what's sent: `BuildSendView` uses the
  consolidated summary + the live tail, never the frozen turns themselves.
- `/c` "show original" and `ExportContext` (which call `BuildLLMHistory` over all
  turns) will show the `[pruned…]` placeholder for aged turns past 90 days — the
  raw aged out by design; the summary carries the substance.
- No pairing impact: a stubbed turn has empty `content_json` → no tool blocks →
  `BuildLLMHistory` renders it as a `[pruned…]` text turn; `RepairPairing` is
  unaffected.
- A collapsed (180-day) conversation has no turns and no compaction row; if
  somehow resumed, it sends only whatever the (now-absent) history yields —
  acceptable for a 6-month-dead session.

## Error / edge

| Case | Behavior |
|---|---|
| `keep_forever` true | Sweep is a no-op; nothing ages |
| Conversation never compacted | 90-day stage skips it (nothing frozen); 180-day still collapses if dead that long |
| Sweep runs twice on the same conversation | Idempotent — second run prunes/deletes nothing |
| A single conversation errors mid-sweep | Logged, skipped; the sweep continues with the rest |
| Compacted-age < raw-age (misconfig) | Honored as written; a turn could be collapsed before its raw stub — harmless (collapse supersedes) |
| Live turn within the 90-day window | Never pruned (only frozen turns are eligible) |

## Testing

- **Store:** `PruneRawBodies` stubs only frozen turns older than the cutoff
  (leaves recent/live + un-frozen turns intact), is idempotent, and returns the
  count; `CollapseConversation` removes turns + the compaction row but keeps the
  `conversations` row (title/recap survive); both no-op on an empty/absent target.
- **Sweeper (fake store/clock):** with a conversation older than 180d → collapse
  called; one 100d-old compacted conversation → raw stub called on its frozen
  turns; a fresh conversation → neither; `keep_forever` → nothing called. Drives
  the policy deterministically without real time (inject `now`).
- **Engine non-interference:** after pruning a frozen turn, `BuildSendView` still
  returns the same consolidated-summary-+-live-tail view (frozen turns weren't in
  it anyway), and the result stays pairing-valid.

## Out of scope (follow-ons)

- Per-conversation **pin** (a `pinned` column + Pin/Unpin RPC + a `/c`/history
  keybind to exempt a conversation from aging).
- **Size-based LRU** (prune oldest conversations' raw once total raw bytes exceed
  a cap — a secondary gate; age-based already bounds growth to a rolling window).
- A `/c` retention indicator / "this session was pruned" affordance.

## Key file references

| Concern | Location |
|---|---|
| Turn content columns (prune target) | `source/server/internal/conversation/schema.sql` (`turns.content`/`content_json`) |
| Compaction row (collapse target) | `conversation_compaction` table |
| Identity stub (kept) | `conversations.title` / `.recap` |
| New store ops | `source/server/internal/conversation/store.go` (`PruneRawBodies`, `CollapseConversation`) |
| Sweeper | `source/server/internal/retention/` (new package) |
| Generator wiring pattern | `source/server/internal/recap/recap.go`, `internal/compactiongen/` |
| Config | `source/server/pkg/config/config.go` (`CompactionConfig.Retention`) |
| Sweeper wiring | `source/server/cmd/cercano/main.go` |
