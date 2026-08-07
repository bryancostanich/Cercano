# Fix: activity indicator causes scrollback to "bounce"

## Problem

Cercano recently gained a live activity indicator in the chat transcript — the
trailing "still working" line (`thinking` / `routing` / `running <tool>` /
`writing`, plus elapsed time, tokens, and engine) that appears while a turn is
in flight but nothing visible is streaming. It fixed a real UX gap (the agent
would go dark and prompts silently queued with no signal anything was
happening).

But it introduced a new problem: the indicator **shows and hides repeatedly
within a single turn**, and each toggle changes the transcript's line count,
which makes the surrounding scrollback content jump up and down — a "bounce"
that makes text hard to read while it streams or while it just sits there.

## Root cause (traced)

The trailing activity line is not separate screen chrome — it is appended to the
transcript content itself, inside `chatView.rebuild()`:

- `source/clients/cli/internal/ui/chat_view.go` ~L1060–L1073: when
  `IsBetweenPhases()` is true, `rebuild()` writes a `"\n\n"` blank separator
  followed by `renderTrailingActivity(...)` onto the assembled viewport content.
  That is **three content rows** (blank, blank, the line) added to the bottom.
- `IsBetweenPhases()` (`chat_view.go` L868–L903) flips true only when the turn
  is streaming AND the current phase has gone quiet (empty streaming
  placeholder, or a text entry whose `lastTokenAt` is older than
  `staleStreamThreshold`). As soon as tokens flow again, or a tool result / new
  entry lands, it flips back to false and those three rows vanish.
- The viewport is pinned to the bottom during streaming (`wasAtBottom` →
  `GotoBottom()` at the end of `rebuild()`, `chat_view.go` ~L1088). So adding /
  removing three rows at the pinned tail visibly shoves all the content above it
  up and down. That is the bounce.

Within one multi-step turn the indicator can toggle many times (each quiet gap
between a prose segment and the next tool call, each stall between phases),
producing repeated 3-row jumps.

## Desired behavior

When the activity indicator appears and then goes away, **preserve the vertical
space it occupied** (leave it as empty rows) until genuinely new streaming
content arrives to fill that space. The transcript must not shrink back the
moment the indicator hides — only real new content reclaims the reserved rows.
Net effect: the tail region stays visually stable; no bounce.

## Scope / constraints

- Change is confined to the CLI transcript rendering
  (`source/clients/cli/internal/ui`, chiefly `chat_view.go`). No server or
  protocol changes.
- Must not regress the original fix: while the agent is working with nothing
  visibly streaming, the user must still see the live activity line (spinner +
  verb + elapsed/tokens).
- Must not leave a permanent blank gap after a turn fully completes — reserved
  space is a within-turn stabilizer, not a persistent artifact. Once the turn
  ends (streaming stops) the transcript settles to its natural height.
- Preserve existing behaviors: resize anchoring (`hasResizeAnchor`), bottom
  pinning when `wasAtBottom`, queued-message chrome, selection/copy row maps
  (`arrowRows`, `linkRows`, `plainLines`).
- No golden-test breakage for static (non-animated) frames; the trailing
  activity branch is already excluded from goldens because it is time-animated.

## Acceptance

1. During a multi-step streaming turn, the transcript tail does not visibly jump
   up/down as the activity indicator toggles between phases. (Manual: run a
   multi-tool prompt and watch the scrollback while phases change.)
2. When the agent is quiet mid-turn, the live activity line is still shown.
3. When new prose or a new entry arrives, it fills the previously-reserved space
   (no double gap, no lost rows).
4. After a turn completes, the transcript has no leftover reserved blank tail.
5. `cd source/clients/cli && go test ./... -count=1` passes, including any new
   test that asserts the tail height is stable across an indicator show→hide
   toggle within a live turn.
