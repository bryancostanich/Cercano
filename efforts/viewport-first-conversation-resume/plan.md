# Plan: Viewport-first conversation resume with eager backfill

## Phase 0 — Baseline and branch/worktree setup

1. Work in a dedicated worktree/branch from current `main` before implementation.
2. Confirm clean starting state and record HEAD.
3. Add temporary timing probes only after reducing the current load bottleneck to measurable paths:
   - current `applyResume` wall time with a large persisted transcript;
   - current `chatView.SetEntries`/layout time for the same transcript;
   - current client resume collection behavior.
4. Remove temporary probes before committing.

Verification gate:

- Baseline numbers captured in implementation notes or commit body.

## Phase 1 — Persistence paging primitives

1. Extend the persistent conversation store with paged turn retrieval while preserving existing `GetTurns` behavior.
2. Add APIs along these lines:
   - `CountTurns(ctx, conversationID string) (int, error)` or include count in page results.
   - `GetTurnPage(ctx, conversationID string, start, limit int) ([]Turn, error)` for chronological indexed pages.
   - Or `GetTailTurns(ctx, conversationID string, limit int) (turns []Turn, startIndex int, total int, err error)` plus `GetTurnsBefore(ctx, conversationID string, beforeIndex, limit int)`.
3. Prefer stable insertion order by using the existing ordered turn query semantics. If SQLite `rowid` is the real ordering key today, expose that through deterministic query ordering rather than relying only on same-second timestamps.
4. Add store tests for:
   - tail page order;
   - older page order;
   - empty conversation;
   - page boundaries larger than total;
   - same-second insertion order.

Verification gate:

- `go test ./internal/conversation` in the server module, or the package-local equivalent.

## Phase 2 — Proto and server tail-first stream

1. Add a new RPC to `source/proto/agent.proto`, for example:
   - `rpc StreamResumeConversationViewportFirst(ResumeConversationViewportFirstRequest) returns (stream ResumeConversationViewportFirstEvent)`.
2. Include request fields for:
   - `conversation_id`;
   - tail budget, likely `tail_turns` and/or byte target;
   - older chunk budget.
3. Include stream event fields for:
   - `conversation_id`;
   - `turns` for a page;
   - `start_index` and `total_turns` or equivalent page range metadata;
   - `tail`/`older` page kind;
   - `backfill_complete`;
   - `hydration_complete`;
   - optional human-readable error/status.
4. Implement the service so it:
   - sends the tail page first from persistence without waiting for full session hydration;
   - starts session/context hydration concurrently using the existing resume code path or an extracted hydration helper;
   - eagerly streams older pages after the tail page;
   - emits hydration completion when server session state is ready for a new turn;
   - emits backfill completion when all older pages have been sent;
   - respects stream context cancellation.
5. Keep existing `ResumeConversation` and `StreamResumeConversation` semantics unchanged.
6. Add service tests for stream order, backfill completion, hydration completion, and old API compatibility.

Verification gate:

- Server package tests covering persistence/service/proto compile.

## Phase 3 — Agentclient progressive API

1. Add an agentclient method that exposes progressive events to the CLI instead of collecting all chunks before returning.
2. Shape options:
   - callback-based method taking `func(ResumeViewportEvent) error`; or
   - channel-returning command helper used by the UI.
3. Prefer a callback or channel that preserves streaming order and lets the caller update the UI after each chunk/batch.
4. Keep existing `ResumeConversation` client method unchanged.
5. Add agentclient tests proving the first tail event is delivered before later/backfill events and before hydration completion if hydration is delayed.

Verification gate:

- Server/client package tests pass.

## Phase 4 — CLI resume state machine

1. Replace synchronous `applyResume` use from history selection with an asynchronous Bubble Tea command.
2. Add explicit model state, likely:
   - `resumeLoading bool` for initial tail wait;
   - `resumeHydrating bool` until server readiness;
   - `resumeBackfilling bool` until older chunks complete;
   - `resumeConversationID string`/generation token to drop stale events;
   - optional pending status/error string.
3. On history selection:
   - close the history/content page;
   - switch to main chat;
   - set a visible loading entry/banner;
   - start the viewport-first resume command;
   - keep prompt editable but block submit until hydration completes.
4. Handle progressive resume messages:
   - tail page: install tail entries and older-loading sentinel, anchor bottom;
   - older page: prepend older entries before the loaded range, preserving viewport anchor;
   - hydration complete: clear submit gate/status;
   - backfill complete: remove sentinel and mark transcript complete;
   - error: show visible system error and clear/loading state safely.
5. Ensure rollover resume and explicit `--resume` startup paths either use the new progressive path or intentionally stay synchronous with a documented reason. Prefer using the same path for main conversation resume.

Verification gate:

- UI tests for state transitions and submit gating.

## Phase 5 — Chat view progressive transcript APIs

1. Add chat view APIs for progressive load without requiring callers to mutate internals:
   - `BeginProgressiveLoad(tail []*Entry, hasOlder bool)`;
   - `PrependProgressiveEntries(entries []*Entry)`;
   - `CompleteProgressiveLoad()`;
   - or equivalent names.
2. Represent the older-loading marker as a real layout unit/entry so selection/rendering remains honest.
3. Preserve scroll anchors when prepending:
   - if at bottom before prepend, remain at bottom;
   - otherwise increase `YOffset` by the number of inserted rendered lines so the same content stays visible.
4. Avoid rebuilding on every individual turn. Apply pages/chunks in batches and rebuild once per applied chunk.
5. Recompute link rows and arrow rows consistently after each chunk. If this proves too costly, first optimize by chunk coalescing; do not introduce stale link behavior silently.
6. Add tests for:
   - sentinel visible while incomplete;
   - final chronological transcript after backfill;
   - bottom anchoring;
   - non-bottom anchor preservation;
   - link/selection behavior after prepend.

Verification gate:

- `go test ./internal/ui` focused resume/progressive tests pass.

## Phase 6 — Eager backfill cadence and responsiveness

1. Stream older chunks eagerly from server immediately after tail.
2. In the CLI, coalesce application if chunks arrive faster than the UI should relayout.
3. Use chunk-level updates rather than per-turn updates. Start with existing byte-budgeted chunk sizes or a conservative turn-count/byte hybrid.
4. Add a benchmark/probe for large transcript progressive load:
   - time to first tail render;
   - total backfill time;
   - prompt keypress during backfill;
   - scroll stability during backfill.
5. Keep backfill cancellable if the user switches to another conversation before completion.

Verification gate:

- Performance probe shows first tail render is decoupled from full transcript size.
- Prompt keypress during backfill remains in the same order of magnitude as prompt-only keypress, not tens of milliseconds.

## Phase 7 — Full verification and cleanup

1. Remove all temporary probes.
2. Run focused package tests:
   - server conversation/store tests;
   - server host service resume tests;
   - agentclient resume tests;
   - CLI UI resume/progressive tests.
3. Run broader gates:
   - server module `go test ./...` if proto/server changed;
   - CLI module `go test ./internal/ui`;
   - CLI module `go test ./...`;
   - CLI `make build`.
4. Capture benchmark before/after in commit body.
5. Checkpoint the effort with a conventional commit.
6. Do not push unless explicitly requested.

## Risks and mitigations

- Risk: user submits before hydration is complete.
  - Mitigation: explicit submit gate and status hint; tests around Enter handling.
- Risk: prepending older chunks yanks scroll position.
  - Mitigation: anchor tests for bottom and non-bottom states.
- Risk: existing resume callers break.
  - Mitigation: new RPC; old APIs unchanged; compatibility tests.
- Risk: server hydrates and display stream race on shared state.
  - Mitigation: tail/backfill reads from persistence only; hydration completion is signaled independently.
- Risk: relayout per chunk still causes noticeable stalls.
  - Mitigation: chunk coalescing/debounce and performance probes.

## Implementation notes from exploration

- Current proto has `ResumeConversation` and `StreamResumeConversation`; comments explicitly say the stream returns the same logical resume transcript as unary but in bounded batches.
- Current service `StreamResumeConversation` calls `x.convAgent.ResumeConversation(...)` before streaming chunks, so it cannot provide immediate tail display.
- Current agentclient `ResumeConversation` collects all stream chunks into one result.
- Current store has `GetTurns(ctx, conversationID)` chronological full retrieval and tests preserving insertion order for same-second timestamps.
- Current `chatView.SetEntries` rebuilds layout and link rows for the full concrete transcript.
