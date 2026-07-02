# Non-blocking compaction + bounded compacted view — Design

**Fixes the 2026-07-02 session-stall incident.** Root cause chain, established by
live investigation (server log, lsof, Ollama state, conversations.db):

1. **The wedge.** `assembleHistory` (`internal/server/server.go`) runs on the
   request path before every turn. When the send view exceeds
   `hard_override_pct` of the cloud model's max context, it calls
   `agent.CompactNow` **synchronously** — no timeout, no logging. The summarizer
   runs on the local provider (a 79.7B model when `summarizer_model` is unset),
   so the turn blocks for effectively unbounded time. Ollama serializes
   requests, so every other session queues behind the first. Sessions appear
   "stalled talking to claude" while never reaching the cloud.
2. **The accumulation.** Compaction's own output is unbounded: the incident
   conversation had `compacted_tokens = 338,394` — the *compacted view* itself
   was 338k tokens (2,147 turns; segment summaries accumulate and are never
   re-consolidated). A view permanently over the hard limit re-fires the
   synchronous pass on every turn.
3. **The silence.** Compaction passes had also stopped completing (last success
   two days before the incident — the window where the local lane was
   misconfigured during llama-server testing), and nothing logs when a pass
   starts, finishes, or fails.

**Design directive (Bryan):** compaction work must never block a turn. The
request path may *trigger* compaction, but only as an asynchronous kick.

## 1. The request path never runs compaction inline

`assembleHistory` keeps its role as the breach *detector* (it is where the
token count is already computed) but the response to a breach changes:

- **Kick, don't run:** replace the `s.agent.CompactNow(ctx, convID)` call with
  the agent's existing non-blocking `ScheduleCompaction` path
  (`compactiongen.Generator.Schedule`) — the already-built background
  generator with per-conversation debounce, in-flight dedup, a 2-minute
  `runTimeout`, and swallow-failure semantics. No new goroutine machinery.
- **Degrade this turn mechanically — zero model calls:** after the kick, bring
  the current turn's send view under the hard limit with LLM-free steps, in
  order, stopping as soon as it fits:
  1. `ElideSupersededToolResults` (exists; lossless),
  2. `KeepLastNToolResults` (exists; lossy but content-preserving for recent
     work),
  3. **oldest-turn truncation** (new, small): drop whole messages from the
     front of the view until it fits, never splitting a message and never
     dropping the leading consolidated-summary block when one exists.
- The turn then proceeds normally. When truncation (step 3) fires, log one line
  with the conversation id and dropped-token count.

`CompactNow` itself survives only for explicit callers (the `/compact`-style
command path, tests); it is no longer reachable from the turn path.

## 2. Bound the compacted view (the real accumulation fix)

The compactor gains an **output budget**: compaction must move the view
*toward* a target size, never merely append to it.

- Budget: `activation_floor_tokens` (the existing config value — the size at
  which compaction begins is also the size it aims back down to). No new
  config knob.
- Mechanism: when `consolidated + segment_summaries` exceeds the budget after
  a normal pass, run a **re-consolidation** step in the same background pass:
  summarize the summaries (the map-reduce fold the compaction package already
  models) so old segments collapse into the consolidated block instead of
  accumulating forever.
- Invariant, enforced with a test: for any input state, a *successful* pass
  yields `compacted_tokens` strictly less than the pre-pass view when the
  pre-pass view exceeds the budget. (A pass that cannot shrink logs and
  reports failure rather than silently persisting growth.)

## 3. Observability

- The background generator logs pass start (`conversation, pre-pass tokens`),
  success (`post-pass tokens, duration`), and failure (`error, duration`) to
  stderr — the two-day silent failure becomes impossible to miss.
- The hard-override breach in `assembleHistory` logs when it fires (id,
  view tokens, hard limit) and which degrade steps were applied.
- A warn-level line at generator construction when no `summarizer_model` is
  configured (the summarize lane will use the interactive local model, which
  may be very slow for large histories).

## 4. Out of scope (verified or deferred)

- **Recap/llama-server misrouting:** the `[recap] … not found in model_dirs`
  failures trace to the runtime being switched to llama_server during
  install-modal testing; the lane self-heals on switch-back. Verify after the
  server restarts on fixed code; no blind fix.
- Choosing/shipping a default small `summarizer_model` (config/UX decision).
- The context meter's own accounting (Bryan's earlier `estimateRawTokens` fix
  stands; the 369.6k reading was an honest measurement of an genuinely
  bloated view).

## Testing

- `assembleHistory` breach path: over-limit view → Schedule called (spy),
  turn's view comes back under the limit via mechanical steps only (no
  summarize call), truncation preserves the summary block and message
  boundaries, log lines emitted.
- Compactor budget: an over-budget state re-consolidates to under budget
  (stubbed summarize); the monotonic-shrink invariant; a shrink-failure logs
  and errors instead of persisting growth.
- Generator logging: start/success/failure lines (stubbed summarize).
- Regression: both modules' full suites; the existing compaction tests keep
  passing.
