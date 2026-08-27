# Plan: Integrate Virtualized Chat Viewport

## Phase 1 — Establish the current baseline

1. Confirm the checkout is on clean `main`.
2. Add a temporary or permanent focused benchmark that measures the current `chatView.View()` hot path on a large transcript.
3. Run the benchmark once before porting and save the result in notes or test output.
4. Remove any temporary probe if it is not intended to be committed.

Acceptance for this phase:
- Current baseline is measured directly from `main`.
- No implementation changes have been made yet.

## Phase 2 — Port the virtual scroll primitive

1. Add `chat_virtual_scroll.go` from the old branch, adjusted only as needed for current code style.
2. Add or port `chat_virtual_scroll_test.go`.
3. Keep the public `chatView` scroll method surface unchanged:
   - `Width`
   - `Height`
   - `TotalLineCount`
   - `YOffset`
   - `SetYOffset`
   - `AtBottom`
   - `GotoBottom`
   - `ScrollUp`
   - `ScrollDown`
4. Run the virtual scroll tests.

Acceptance for this phase:
- `virtualScroll` tests pass.
- No Bubble viewport replacement is active yet.

## Phase 3 — Port transcript layout units

1. Add `chat_layout.go` from the old branch, adapted to current `main` render-cache functions and animation-overlay state.
2. Represent the transcript as ordered render units:
   - normal entry units,
   - contiguous tool group units,
   - separator units,
   - trailing activity units.
3. Store rendered unit lines so visible-window reads do not split or scan one giant transcript string.
4. Preserve range-based helpers from the branch where applicable:
   - `linesRange`
   - `lineAt`
   - `arrowRowAt`
   - `absoluteArrowRows`
5. Adapt current `chat_render_cache.go` to expose cached rendered lines where helpful, but avoid broad markdown-rendering changes.

Acceptance for this phase:
- Layout unit construction compiles.
- Existing render-cache behavior remains intact.
- No current-main animation semantics are changed.

## Phase 4 — Replace Bubble viewport ownership inside `chatView`

1. Replace the transcript-owned `viewport.Model` state with:
   - `layout transcriptLayout`,
   - `scroll virtualScroll`.
2. Keep current `chatView` external methods stable so callers do not need broad rewrites.
3. Update `SetSize` to use `virtualScroll.SetSize` and preserve resize anchoring.
4. Update `SetEntries` to rebuild `transcriptLayout`, update total line count, and preserve bottom/resize anchoring.
5. Update `View` to materialize only visible lines from `layout.linesRange` and then apply current-main behavior:
   - visible-row animation overlay or equivalent dynamic-unit refresh,
   - selection highlighting,
   - width truncation,
   - padding,
   - scrollbar column.
6. Preserve current animation-only tick behavior in `model.go`: animation ticks must not call `refreshViewport()` unless content is dirty.

Acceptance for this phase:
- `chatView.View()` no longer calls Bubble viewport `View()` or depends on `viewport.SetContent()` for transcript drawing.
- Prompt typing and animation-only ticks do not rebuild transcript layout unless content/size/scroll/selection state requires it.

## Phase 5 — Update selection, links, folds, tools, and scrollbar interactions

1. Update plain-line materialization to use `layout.lineAt` / range helpers instead of splitting a giant content string.
2. Update selection hit-testing and copy behavior to use range-aware plain-line helpers.
3. Update arrow/fold hit-testing to use `layout.arrowRowAt` instead of scanning a flat `arrowRows` slice where appropriate.
4. Update scrollbar drag/click behavior to use `virtualScroll` dimensions and offsets.
5. Update link-row collection strategy:
   - preserve current link interaction semantics,
   - avoid whole-transcript link scans on every rebuild where possible,
   - at minimum avoid reintroducing worse behavior than current `main`.
6. Update tests that directly used `c.vp.SetContent` to use `SetEntries`, layout setup helpers, or a test-only content setter.

Acceptance for this phase:
- Existing tests for selection, scrollbar dragging, fold/rail hit-testing, prompt scroll preservation, resize anchoring, and banner visibility pass or are updated to equivalent current behavior.

## Phase 6 — Dynamic visible units and animation correctness

1. Port or adapt `chat_dynamic_view.go` if needed.
2. Ensure dynamic rows are refreshed only for the visible window:
   - empty streaming assistant placeholders,
   - in-progress/loading tool groups,
   - trailing activity rows,
   - banner shimmer if represented as a dynamic unit.
3. Preserve row stability. If dynamic text changes row count, rebuild layout once and clamp/anchor offsets.
4. Keep the current-main test contract from the latest animation fix:
   - animation-only progress ticks leave cached transcript/layout content untouched unless visible dynamic unit shape changes.

Acceptance for this phase:
- The current animation tests pass.
- Placeholder/tool/trailing animations visibly update.
- Animation-only ticks do not call the old full-transcript `SetContent` path because that path no longer exists for transcript drawing.

## Phase 7 — Benchmarks and performance validation

1. Port/adapt `chat_view_benchmark_test.go` from the old branch.
2. Compare post-port benchmark results against the Phase 1 baseline for:
   - `chatView.View()` over a large transcript,
   - `SetEntries` over a large cached transcript if benchmarked.
3. Optionally inspect recent `~/.config/cercano/tui-perf.log` after a rebuilt CLI run to confirm slow `op=view` / main-buffer symptoms improve.

Acceptance for this phase:
- Benchmarks show a meaningful reduction in `chatView.View()` time and allocations.
- No benchmark/probe files remain unless they are intentional committed benchmarks.

## Phase 8 — Full verification and checkpoint

1. Run focused UI tests first:
   - animation tick tests,
   - chat view tests,
   - selection tests,
   - scrollbar drag tests,
   - resize scroll tests,
   - rail/tool hit tests.
2. Run the full CLI test suite:
   - `cd source/clients/cli && go test ./...`
3. Build the CLI module with the project build target if practical:
   - `cd source/clients/cli && make build`
4. Inspect git status/diff summary.
5. Checkpoint only the integration files with a conventional commit subject and body.
6. Do not push unless explicitly asked.

Acceptance for this phase:
- Full CLI tests pass.
- Build passes or any build-only blocker is clearly reported.
- Work is committed on `main`.

## Risks and mitigations

- Risk: stale branch behavior overwrites current animation fix.
  - Mitigation: port manually and keep current `model.go` tick contract as a test requirement.

- Risk: selection or copy behavior regresses because plain lines are no longer backed by one flat split string.
  - Mitigation: preserve `PlainLines` and range helpers; run selection tests and add tests if gaps appear.

- Risk: direct tests that touch `c.vp` break because `vp` is removed.
  - Mitigation: update tests to use public `chatView` methods or test helpers that seed layout/scroll state.

- Risk: link detection still scans too much.
  - Mitigation: first preserve correctness, then cache/range-scope link rows if needed by benchmark or test evidence.

- Risk: dynamic unit row counts change during animation and desynchronize offsets.
  - Mitigation: visible dynamic refresh returns a shape-change signal and triggers a single layout rebuild/clamp.

## Rollback plan

If the port becomes unstable or fails to meet behavior requirements, revert the integration commit and keep current `main`, which already contains the animation-only rebuild fix. The old branch remains available as reference.
