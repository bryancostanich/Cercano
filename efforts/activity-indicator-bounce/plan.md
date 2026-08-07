# Plan: stabilize the trailing activity indicator (no bounce)

## Where the change lives

All in `source/clients/cli/internal/ui/chat_view.go`, in and around
`rebuild()`/`SetEntries()` (the trailing-activity append at ~L1060–L1073) and
the `chatView` struct. One new small test file.

## The design choice: how to "reserve" the space

The indicator today adds three content rows (`"\n\n"` + one line) at the pinned
tail whenever `IsBetweenPhases()` is true, and removes them the instant it flips
false. The bounce is that add/remove at the bottom. We want the height the tail
occupies to be **monotonic within a turn**: once the indicator has claimed its
rows, hiding it leaves the rows as blank filler until new content overwrites
them; the reserved rows collapse only when the turn ends.

| Decision axis | A. Reserve fixed rows (high-water floor) | B. Ghost/blank-line substitution | C. Debounce the toggle (time-hysteresis) |
|---|---|---|---|
| Mechanism | Track a per-turn "tail reserve" = max rows the indicator has occupied this turn; when the indicator is hidden mid-turn, emit that many blank rows instead | When `IsBetweenPhases` goes false but the turn is still live and no new entry has arrived, render the indicator's rows as blanks (space-filled) rather than dropping them | Keep the indicator visible for a grace period after it would hide, so it stops flickering |
| Kills the bounce? | Yes — tail height never decreases mid-turn | Yes — same row count whether shown or ghosted | Partially — reduces frequency but any real toggle still jumps |
| New content fills the gap? | Yes — real entries render into the space; reserve recomputed from actual content | Yes — blanks are replaced when a new entry lands | N/A — doesn't reserve, just delays |
| Leftover blank after turn ends? | No — reserve resets when `streaming` goes false | No — ghosting only while live | No |
| Complexity | Low: one int field + max()/reset, localized to rebuild() | Low–med: branch in the trailing block + "is this still live" check | Med: timer state, animation ticks, still flickers |
| Risk | Must reset reserve correctly on turn end and on genuine content growth | Ghost rows must not pollute selection/copy plainLines or arrowRows | Doesn't fully solve the stated problem |

**Chosen: A (high-water reserve), which subsumes B.** It directly implements
what the user asked for — "preserve empty space room for it until new streaming
content comes in to fill it." It is the smallest, most predictable change: a
single `tailReserve int` high-water mark on `chatView`, updated in `rebuild()`.
C is rejected because a debounce only lowers bounce frequency; it doesn't
stabilize the layout, and it adds timer state.

## Mechanism (option A) in detail

Add to `chatView`:
- `tailReserve int` — the max number of content rows the trailing activity block
  has occupied so far in the current turn (0 when none yet).

In `rebuild()` / `SetEntries()`, at the trailing-activity block:

1. Compute the indicator's own row cost when `IsBetweenPhases()` is true
   (currently a `"\n\n"` separator + one rendered line = 3 rows; compute it from
   the actual rendered string so it stays correct if the line ever wraps).
2. `tailReserve = max(tailReserve, indicatorRows)` whenever the indicator is
   shown.
3. When `IsBetweenPhases()` is false but the turn is still live
   (`c.streaming == true`) and `tailReserve > 0`, append `tailReserve` blank
   rows instead of nothing — reserving the space so the tail height does not
   shrink.
4. Reset `tailReserve = 0` when the turn ends. The clean signal is `c.streaming`
   transitioning to false; do the reset where `streaming` is set (its setter /
   the host's SetStreaming path) or guard at the top of the trailing block: if
   `!c.streaming` then `tailReserve = 0` and append nothing.

Reserved blank rows must be **pure whitespace** so they:
- do not appear as selectable/copyable text of interest (they still occupy a
  plainLines row, which is fine — blank rows already occur in the transcript),
- carry no `arrowRow`/`linkRow` entries (they are appended after the entry loop,
  same as the indicator is today, so no arrow/link bookkeeping changes),
- get overwritten naturally on the next `rebuild()` once a real entry grows the
  content (the reserve is recomputed each rebuild from the current indicator
  cost and the running high-water, and cleared at turn end).

Bottom-pinning (`wasAtBottom` → `GotoBottom()`) and resize anchoring stay
untouched; because the tail height is now monotonic within a turn, pinning no
longer yanks the content.

## Steps

1. Add `tailReserve int` field to `chatView` (struct in `chat_view.go`) with a
   doc comment explaining the high-water/no-bounce intent.
2. In the trailing-activity block of `rebuild()`/`SetEntries()`:
   - compute `indicatorRows` from the rendered indicator block,
   - update `tailReserve = max(tailReserve, indicatorRows)` when shown,
   - when not shown but `c.streaming`, append `tailReserve` blank rows,
   - when `!c.streaming`, set `tailReserve = 0` and append nothing.
3. Verify the reset path: confirm where `c.streaming` is cleared at turn end
   (grep `streaming` setter) and ensure `tailReserve` is zeroed there or via the
   `!c.streaming` guard above so no blank tail survives a completed turn.
4. Add a focused test `activity_reserve_test.go`:
   - build a `chatView` with a streaming turn, force `IsBetweenPhases()` true,
     rebuild, record `TotalLineCount()`;
   - flip to the hidden-but-still-live state (tokens quiet→resumed OR phase
     boundary) so `IsBetweenPhases()` is false while `streaming` stays true,
     rebuild, assert `TotalLineCount()` did **not** decrease (no bounce);
   - set `streaming=false`, rebuild, assert the reserved blank tail is gone
     (height settles to natural content height).
5. `cd source/clients/cli && go build ./... && go test ./... -count=1`.
6. Manual smoke: run a multi-tool prompt in the CLI, watch the tail stay steady
   across phase changes; confirm the live activity line still shows when quiet
   and no blank gap lingers after completion.
7. Checkpoint with a conventional commit.

## Risks & mitigations

- **Reset missed → lingering blank tail.** Mitigated by the `!c.streaming`
  guard that both resets `tailReserve` and appends nothing, plus acceptance
  test step 4's final assertion.
- **Reserve too tall if the indicator line wraps at narrow widths.** Computing
  `indicatorRows` from the actual rendered block (not a hardcoded 3) keeps it
  correct; on resize, the existing resize-anchor path already re-runs rebuild.
- **Selection/copy drift.** No new arrow/link rows are introduced; blank rows
  behave like the existing blank separators already present in transcripts.

## Out of scope

- Any change to when `IsBetweenPhases()` decides to show the indicator, to the
  `staleStreamThreshold`, or to the indicator's visual styling.
- Server/protocol/telemetry changes.
