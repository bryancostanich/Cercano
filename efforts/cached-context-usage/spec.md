# Cached context usage

## Problem

Loading a large conversation shows a context meter of `0`.

Reproduced with `LUNIE - PROGRESSIVE PER SEGMENT LOD`:

```text
conversation_id: 230386d992670d7e
turns:           6,602
content_json:    ~298 MB
compaction:      frozen_through covers 5,077 turns, consolidated summary ~58 KB
live tail:       1,525 turns, ~74.8 MB JSON
```

`GetContextUsage` recomputes provider-facing accounting on every call:

```go
assembled := x.assembleHistoryForTarget(ctx, convID, requestassembly.Target{Model: x.primaryModel()}, false)
acct = assembled.Accounting
raw = acct.RawTokens
sent = acct.FinalTokens
```

For this conversation that work loads and re-tokenizes the entire live tail. A
probe running exactly this path did not finish within 30 seconds. The CLI polls
with a 2-second deadline in both the footer and the context view:

```go
ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
u, err := ag.GetContextUsage(ctx, convID)
```

On error the client zeroes every meter field, so the user sees a confident `0`
for a conversation that actually holds millions of tokens of raw history.

There is already an in-memory accounting cache (`requestAccounting`), but it is
per-process and only written while serving a turn. Loading a conversation
without running a turn never populates it, which is exactly the reported case.

The expensive numbers are already computed elsewhere and then discarded. Every
compaction path builds the send view and totals it:

```go
postView, err := compactor.BuildSendView(turns, newState)
post := compaction.TotalTokens(g.tok, postView)
```

That value is logged and thrown away, then the meter tries to recompute the same
thing under a 2-second UI deadline.

## Goals

- A loaded conversation with stored history never reports `0` context usage.
- `GetContextUsage` serves the meter without assembling full history.
- Context accounting survives agent restarts.
- The snapshot is refreshed from work already being done, adding no new
  full-history assembly passes.
- The UI can distinguish live accounting from a durable snapshot.

## Non-goals

- Changing compaction policy, thresholds, or the summarizer.
- Changing how `requestassembly.Assemble` computes accounting.
- Making the meter exact for non-OpenAI tokenizers; it stays an estimate.
- Per-target/per-model snapshot history. One current snapshot per conversation.
- Removing the in-memory `requestAccounting` fast path.
- A background recompute worker. Explicitly rejected during design.

## Design direction

### Durable snapshot table

Add `conversation_context_usage`, keyed by `conversation_id`, storing the
accounting fields the RPC already returns plus provenance:

```text
conversation_id     TEXT PRIMARY KEY
tokens_used         INTEGER  -- sent/compacted view
raw_tokens          INTEGER
message_tokens      INTEGER
system_tokens       INTEGER
tool_schema_tokens  INTEGER
output_reserve      INTEGER
estimated_request   INTEGER
context_window      INTEGER
window_known        INTEGER
model               TEXT
source              TEXT     -- 'turn' | 'compaction'
computed_at         INTEGER
```

It is a derived cache, separate from conversation identity and from compaction
state, so it can be cleared or rebuilt independently and never makes a
conversation's existence depend on having been compacted.

### Write points (reuse existing computation)

The snapshot is written where accurate numbers already exist:

- `recordRequestAccounting` — exact provider-facing accounting from a real turn,
  including system and tool-schema tokens. Currently memory-only.
- compaction pass completion in `runCompaction`, after `SaveCompaction`.
- `Regenerate` and `Clear` completion, including the cleared-state case.

No new assembly work is introduced at any write point.

### Read path

`GetContextUsage` resolves in priority order:

1. in-memory `requestAccounting` for this process, when present;
2. the durable snapshot from `conversation_context_usage`;
3. absent snapshot: report unknown rather than a fabricated `0`.

The response gains freshness metadata so the client can render an approximate or
stale state instead of collapsing to zero:

```proto
int64 usage_computed_at   = 12;
string usage_source       = 13;  // "live" | "snapshot" | "none"
bool   usage_stale        = 14;
```

### Client behavior

`agentclient.ContextUsage` carries the new fields. The CLI stops treating a
failed or empty poll as "zero context": it keeps the last known values and marks
them stale rather than clearing them.

## Acceptance criteria

- Loading `230386d992670d7e` shows a nonzero context meter within the existing
  2-second poll deadline.
- `GetContextUsage` performs no full-history assembly when a snapshot exists.
- Restarting the agent and loading a previously compacted conversation still
  shows a nonzero meter before any turn is run.
- A compaction pass persists a snapshot whose `tokens_used` equals the post-pass
  send-view total that pass already computed.
- `Clear` persists the resulting post-clear accounting rather than leaving a
  stale pre-clear snapshot.
- A conversation with no snapshot and no live accounting reports
  `usage_source = "none"` and the UI does not display a confident `0`.
- Existing context-meter tests continue to pass, including
  `TestGetContextUsage_RawIsCheapEstimateNotZero` and
  `TestGetContextUsage_CompactedMeterCountsLiveImagesAsReferences`.
