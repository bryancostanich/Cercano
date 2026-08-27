# Integrate Virtualized Chat Viewport

## Problem / motivation

The main transcript buffer is still expensive to draw even after animation-only ticks stopped rebuilding the whole transcript. Direct probing showed `chatView.View()` itself costs measurable time and allocation on large conversations because the current implementation is still built around Bubble viewport content: `SetEntries` assembles one giant rendered transcript string, `viewport.SetContent` splits and scans it, and `View` asks Bubble viewport to compose the visible body before Cercano applies selection, truncation, padding, and scrollbar chrome.

An older branch, `feat/virtualized-chat-viewport`, already contains a replacement architecture that removes Bubble viewport from the transcript hot path. That branch introduces a transcript layout made of render units and a small virtual scroll model that materializes only visible lines. It does not merge cleanly into current `main` because both sides changed core UI rendering, animation, selection, and tests. The branch also predates the current animation-overlay behavior, so a raw merge risks reintroducing tick-driven transcript rebuilds.

## Goals

- Port the useful architecture from `feat/virtualized-chat-viewport` onto current `main`.
- Replace the Bubble viewport-backed transcript surface in `chatView` with a layout plus virtual scroll surface.
- Preserve current `main` behavior for animation-only ticks: they must not trigger full transcript rebuilds.
- Keep current chat rendering semantics: scroll position, resize anchoring, selection and copy, scrollbar dragging, fold/tool hit-testing, link detection, banner animation visibility, and trailing activity behavior.
- Add/retain focused tests and benchmarks that prove visible-range materialization and main-buffer draw cost are improved.
- Produce an integrated commit on `main`; do not push unless explicitly requested.

## Non-goals

- Do not redesign markdown rendering, tool rendering, or prompt input.
- Do not raw-merge the old branch just to make conflicts compile.
- Do not reintroduce Bubble viewport as the transcript owner.
- Do not land unrelated formatter churn or restart-verification artifacts from the current stash.

## Constraints

- Current branch is `main` at `c55dea5f` and the worktree is clean.
- The old viewport branch is `feat/virtualized-chat-viewport` at `9d3c81bc` with merge base `b5299291d7c9`.
- The old branch is 24 commits ahead of the merge base while current `main` is 383 commits ahead, so integration must be a manual port.
- A temporary merge check showed textual conflicts in:
  - `source/clients/cli/internal/ui/animation_tick_test.go`
  - `source/clients/cli/internal/ui/chat_render_cache.go`
  - `source/clients/cli/internal/ui/chat_view.go`
  - `source/clients/cli/internal/ui/chat_view_test.go`
  - `source/clients/cli/internal/ui/model.go`
  - `source/clients/cli/internal/ui/scrollback_tool.go`
- Existing current-main animation-overlay behavior is load-bearing and must remain: animation-only progress ticks update paint time, not transcript content.
- The integration should keep the public `chatView` method surface used by `Model` and tests, adapting internals behind methods such as `Width`, `Height`, `TotalLineCount`, `YOffset`, `SetYOffset`, `AtBottom`, `GotoBottom`, `ScrollUp`, `ScrollDown`, `Update`, `PlainLines`, and `View`.

## Decisions

### Integration strategy: manual port of the virtualized architecture

Chosen option: manually port the `transcriptLayout` / `virtualScroll` / visible-range architecture from `feat/virtualized-chat-viewport` onto current `main`.

This was chosen over a raw merge or sequential cherry-pick because the branch is old and conflicts with newer rendering, animation, and test work. Manual porting lets us preserve current behavior while adopting the performance-critical data model.

| Decision axis | Raw merge old branch | Cherry-pick 24 commits | Manual port architecture |
|---|---|---|---|
| Cost | Medium upfront, but conflict resolution is chunky. | Medium to high because conflicts recur across old intermediate commits. | Higher inspection cost, but changes can be shaped to current `main`. |
| Risk | Higher risk of reintroducing obsolete animation rebuild behavior. | Medium risk from resolving obsolete intermediate states. | Lowest behavioral risk because current semantics remain the baseline. |
| Reward | Fastest route to a compiling branch if conflicts are simple. | Preserves old history and intent commit by commit. | Best chance of a clean, maintainable integrated result. |
| Side effects | Could overwrite newer fixes in `model.go` and `chat_view.go`. | Could spend time on commits whose final state is all that matters. | Final history will cite the branch but not preserve every old commit. |
| Hack flags | “Resolve conflicts until green” can hide stale behavior. | Conflict fatigue may produce accidental compromises. | Must be disciplined to avoid omitting a subtle old-branch invariant. |

The approved path is not the fastest textual merge; it is the cleanest in-scope path because the current app behavior is the source of truth and the old branch is a reference implementation for the viewport internals.
