# Spec: Viewport-first conversation resume with eager backfill

## Problem

Loading a long persisted conversation can leave the chat buffer visibly empty for seconds. The viewport renderer is now virtualized once transcript state exists, but the resume path is still eager and blocking:

- The CLI calls resume from inside the Bubble Tea update path.
- The current client API collects all streamed resume chunks before returning.
- The server-side stream hydrates the in-memory session first, then sends chronological chunks.
- The CLI then builds entries/layout/link metadata for the entire transcript before the user sees the resumed chat.

This defeats the viewport work for cold resume: the user waits for full persistence read, session hydration, client collection, and eager UI layout before first useful paint.

## Desired behavior

When the user selects a long conversation from history:

1. The CLI should leave history view and show the latest visible transcript tail quickly.
2. Older history should be fetched eagerly in the background without waiting for the user to scroll.
3. The prompt/UI must stay responsive while older chunks load.
4. The next user submission must not be sent until the server has safely rehydrated conversation context/session state.
5. The eventual transcript should become complete and behave like a normal concrete chat transcript: selection, links, scrollbars, tool rows, and copy should work after backfill.

## Approved design decisions

### New API instead of changing existing resume semantics

Add a new tail-first streaming resume API rather than overloading `StreamResumeConversation`.

Rationale:

- Existing `ResumeConversation` and `StreamResumeConversation` are compatibility APIs that return the same logical transcript in chronological order.
- Tail-first resume has a different contract: latest chunk first, older chunks later, plus readiness/progress metadata.
- A distinct API keeps old callers stable and makes the new progressive UI contract explicit.

### Tail display must not wait for server hydration

The server should send display chunks before full in-memory session/context hydration completes. Hydration runs concurrently. The CLI renders the transcript tail immediately but gates prompt submission until the server reports resume readiness.

Rationale:

- If hydration blocks the first chunk, the blank-buffer symptom remains.
- Correctness is preserved by modeling a `resumeHydrating`/`resumeReady` state in the CLI and disabling or deferring submit while hydration is incomplete.

### Hybrid concrete transcript with older-loading sentinel

The CLI should keep a concrete `[]*Entry` transcript, initially populated with the newest tail plus an explicit older-history loading sentinel at the top. Older chunks are eagerly prepended as they arrive. The sentinel disappears when backfill completes.

Rationale:

- This gives immediate display and eager completion without converting the renderer into a sparse paged document engine.
- It preserves most existing chat view assumptions.
- The sentinel makes incomplete history honest instead of making the tail look like the beginning of the conversation.

### Backfill cadence

Backfill should be eager but UI application should be coarse/debounced. The transport can stream chunks continuously, but the CLI should apply older chunks in batches large enough to avoid per-turn relayout churn and should coalesce UI updates when possible.

## User-visible behavior

- Selecting a long history row immediately closes the history page and returns to chat.
- The chat shows a status/system row such as `loading conversation…` while waiting for the first tail chunk.
- When the tail chunk arrives, the bottom of the conversation is shown and the scroll position is anchored at bottom.
- While older chunks are still loading, the top of the loaded transcript shows an older-history loading marker.
- Status/prompt indicates `rehydrating conversation…` or equivalent until server readiness is confirmed.
- If the user types during hydration, local prompt editing remains responsive.
- If the user presses Enter before readiness, the app should not send the prompt. It should either keep the text in the prompt and show a short status hint, or queue only if an explicit queue policy is implemented. First implementation should not silently queue.
- As older chunks arrive, they are prepended without yanking the visible viewport.
- If the user is at bottom, they remain at bottom.
- If the user scrolled into loaded history, visible content stays stable while older rows insert above.
- Once backfill completes, the marker is removed and scrollback is complete.

## Non-goals for first implementation

- Do not replace the whole chat renderer with a sparse paged document engine.
- Do not remove or change the existing unary `ResumeConversation` compatibility API.
- Do not change normal live streaming semantics.
- Do not make sub-agent tab resume tail-first in the first pass unless it naturally falls out with low risk; main conversation resume is the priority.
- Do not push without explicit user request.

## Acceptance criteria

Functional:

- Long conversation resume renders the latest tail before all turns are fetched/backfilled.
- Older chunks are fetched eagerly after the tail chunk without requiring scroll.
- Prompt typing remains responsive while backfill/hydration runs.
- Submitting a new prompt is blocked until server hydration is complete.
- Existing full resume APIs and tests keep their chronological semantics.
- The final transcript after backfill contains all turns in correct chronological order.
- Scroll anchoring is preserved when older chunks are prepended.

Performance:

- Add a benchmark or probe demonstrating initial tail-to-display path does not scale with full conversation length in the same way as current eager resume.
- Avoid per-turn UI relayout during backfill; apply chunks in batches.

Tests:

- Store tests for tail/paged retrieval order and boundaries.
- Server/handler tests for tail-first stream ordering and hydration-ready signaling.
- Agentclient tests for progressive chunk delivery rather than collect-all behavior.
- UI tests for immediate tail render, older-loading sentinel, eager backfill completion, submit gating while hydrating, and scroll anchor preservation.

## Open details to settle during implementation

- Exact proto message names and fields.
- Tail chunk sizing policy: turn count, byte budget, or both.
- Whether readiness is a final stream event or a field on every chunk.
- Whether the server streams older chunks newest-to-oldest pages or sends each page chronological while pages move backward. Preferred: each page should be chronological internally, with a page/range marker so the client prepends pages correctly.
- Whether sub-agent tabs keep old full resume initially or share the progressive API after main is working.
