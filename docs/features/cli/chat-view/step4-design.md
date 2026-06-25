# Chat View Migration — Step 4 Design (Adopt `chatView` in `/c`, delete `chatPane`)

**Status:** Design — forks ENUMERATED, not decided. The autonomous-run controller
decides each fork (G1…G7) at execution time per
`khalkulo/workflow/design_decisions.md`.

## Goal (per `design.md` step 4)

`/c` (the context manager, `context_view.go`) adopts the SAME `chatView`
(`chat_view.go`) the main page now drives. Retire the thin `chatPane`
(`chatpane.go`) + `renderChatEntry`. End state: ONE chat component, two
independent instances (main + `/c`), each driven by its own `ChatDriver`.

Paths below are `source/clients/cli/internal/ui/` unless noted.

---

## 1. Scope inventory — what `/c` needs vs what `chatView` provides today

`/c` is a single-shot, NO-token-streaming chat: `Submit` → one RPC →
(`chatConfirmMsg` | `chatDoneMsg` | `chatErrorMsg`). It needs a small subset of
what `chatView` offers, plus two things `chatView` does NOT do.

| `/c` need | chatPane today | chatView today | Status |
|---|---|---|---|
| Append user entry on submit | `Submit` (chatpane.go:126) | host appends; no `Submit` | GAP-A |
| Busy "working…" line | `busy bool` + pinned status (chatpane.go:338) | placeholder Entry via `turn` (chat_view.go:555) | GAP-B |
| Whole-message assistant append | `chatAssistantMsg` arm (chatpane.go:147) | NO arm; delta only | GAP-C |
| Confirm (`chatConfirmMsg`) | host-routed (model.go:1775) | host-routed; same gate | OK |
| FIFO queue + unstage | pane-owned (chatpane.go:100,167,179) | `Queued/Enqueue/DrainNext/UnstageLast` (chat_view.go:304-333) | OK (re-point) |
| Grow-to-content height (cap ½) | `DesiredHeight` (chatpane.go:269) | NONE — fixed `vp.Height()` | GAP-D |
| Turns-list ABOVE + dual scrollbar | `regionHeights` (context_view.go:176) drives pane band | n/a (component-agnostic) | GAP-D |
| Transcript + own scrollbar `View()` | chatpane.go:299 | chat_view.go:479 | OK |

### Sharpening finding (drives G2)

`chatAssistantMsg` has **no production emitter**. `contextManagerDriver`
(`context_manager_driver.go`) emits only `chatConfirmMsg`, `chatDoneMsg{text}`,
`chatErrorMsg`. The whole-message-append path is exercised **only by tests**
(`chatpane_test.go` `fakeDriver`, `context_view_layout_test.go:54`). In real `/c`:

- assistant prose arrives via `chatConfirmMsg.assistant` → `appendAssistant`
  (model.go:1776) and via `chatDoneMsg.text` → system entry (chatpane.go:151).
- `chatStatusMsg` likewise has no `/c` production emitter; only
  `main_agent_driver.go` emits it. `/c`'s busy line is set by `Submit`
  (`activity = "working…"`, chatpane.go:134), never by a driver event.

So the only LIVE `/c` whole-message surface is `chatDoneMsg.text` (closing system
line) + `appendAssistant` (pre-confirm rationale). `chatAssistantMsg` is dead in
production — but the tests assert it, so a decision must cover them.

### The four real GAPS (chatView lacks what chatPane did)

- **GAP-A — `Submit`/busy-set:** `chatView` has no `Submit`; the main host calls
  `driver.Submit` itself and opens a streaming placeholder. `/c` currently leans
  on `chatPane.Submit` to append the user entry, flip `busy`, set
  `activity="working…"`, and batch the anim tick (chatpane.go:126-138).
- **GAP-B — explicit busy line:** `chatPane` shows the busy line from an explicit
  `busy bool` + `activity` string. `chatView` shows "working…" ONLY through a
  streaming-assistant placeholder Entry (`Streaming && Content==""`) reading
  `c.turn` (turnStatus), rendered at chat_view.go:555-563. There is no `Busy()`
  and no `busy` field on `chatView`.
- **GAP-C — whole-message arm:** `chatView.Apply` has no `chatAssistantMsg` case;
  its `chatDoneMsg` arm assumes a streaming entry exists (chat_view.go:267-281)
  and falls back to `m.text` only into an open/new assistant entry — it never
  appends a `RoleSystem` "removed N turns." line the way `chatPane` does
  (chatpane.go:151).
- **GAP-D — grow-to-content sizing:** `chatView.View()` always emits exactly
  `vp.Height()` rows; it has no `DesiredHeight()`. `/c`'s `regionHeights`
  (context_view.go:176-191) depends on `pane.DesiredHeight()` to size the chat
  band to its content (capped at ½ panel) so an empty pane doesn't eat the panel
  and a growing chat steals from the turns list.

---

## 2. The forks (ENUMERATE — controller decides; do NOT pick winners)

4-dimension symmetric quantification per `design_decisions.md`:
cost · risk · reward · side-effects. Hacks flagged.

### G1 — Busy / working-state mapping (GAP-A + GAP-B)

How does `/c`'s single-shot "working…" busy state render through `chatView`?

- **G1-a — Driver/host opens a streaming-placeholder Entry.** On submit, host
  appends `&Entry{Role:RoleAssistant, Streaming:true}` and sets
  `chat.SetTurnStatus(turnStatus{activity:"working…", start:now})`; `chatDoneMsg`
  closes it (chat_view.go:267). Reuses the exact main-chat busy path.
  - cost: low (host wiring only). risk: low (proven path). reward: zero
    `/c`-specific render code; one busy concept. side-effects: `/c` now shows the
    spinner+lime-sweep placeholder instead of the pinned bottom status line —
    visible UX change; the placeholder lives in-transcript, not pinned.
- **G1-b — Add explicit `busy bool` + `Busy()` + pinned status line to `chatView`.**
  Port `chatPane`'s pinned-row View concept into `chatView.View`.
  - cost: med (new field + View branch + height accounting). risk: med (touches
    the main-chat View golden surface). reward: byte-closer to old `/c`.
    side-effects: re-introduces the dual busy concept the migration is deleting;
    main chat gains an unused field. **HACK-ish** (un-unifies).
- **G1-c — `/c` keeps a thin busy flag in `contextView`, not in `chatView`.**
  `contextView.busy` gates the status row rendered by `contextView.View`, outside
  `chatView`.
  - cost: low-med. risk: low. reward: `chatView` stays single-concept.
    side-effects: `/c` re-implements a sliver of pane chrome it was trying to drop;
    two render owners in `/c`.

### G2 — Whole-message append vs delta (GAP-C)

`/c`'s live text is `chatDoneMsg.text` + `appendAssistant`; `chatAssistantMsg` is
test-only. How does `chatView` cover the whole-message surface?

- **G2-a — Add a `chatAssistantMsg` arm to `chatView.Apply`** (append a complete
  `RoleAssistant` entry). Unified component handles both whole-append and
  delta-extend.
  - cost: low (one case). risk: low. reward: keeps existing `/c` tests' event
    vocabulary; symmetric component. side-effects: a second text-ingest path in
    the component (delta + whole) — mild conceptual weight.
- **G2-b — Migrate `contextManagerDriver` to emit deltas** (single
  `chatAssistantDeltaMsg` carrying the whole text + a `chatDoneMsg`).
  - cost: med (driver rewrite + `appendAssistant`/confirm-rationale path). risk:
    med (confirm rationale currently goes through `appendAssistant`, not Apply).
    reward: ONE text path in `chatView`. side-effects: forces a streaming mental
    model onto a non-streaming driver; `chatConfirmMsg.assistant` still needs a
    home (see G3).
- **G2-c — Map `chatDoneMsg.text` + `appendAssistant` onto existing `chatView`
  arms; drop `chatAssistantMsg` entirely.** `chatDoneMsg{text}` with no open
  entry → append a `RoleSystem` line (today's arm appends `RoleAssistant`); add a
  `chatView.AppendSystem`-style helper for the confirm rationale.
  - cost: med (adjust `chatDoneMsg` arm for the no-stream case + delete dead
    event). risk: med (the main-chat `chatDoneMsg` arm is shared — changing its
    no-open-entry behavior touches main chat; see G5). reward: removes a dead
    event type. side-effects: couples a `/c` need to the shared `chatDoneMsg` arm.

### G3 — Confirm path

`/c` uses `chatConfirmMsg{onYes,onNo}` (model.go:1775 raises `pendingConfirm`);
main uses `permissionRequiredMsg`→`toolConfirm` (model.go:816). Both host-routed
to the same `confirmRequest` gate.

- **G3-a — Keep `/c` on `chatConfirmMsg`.** Leave `routeChatMsg` confirm handling
  as-is; only swap `cv.pane.appendAssistant` for a `chatView` append.
  - cost: low. risk: low. reward: minimal churn; two confirm event shapes coexist
    (already true). side-effects: `chatConfirmMsg` survives as a `/c`-only event.
- **G3-b — Unify both onto one confirm event.** Collapse `chatConfirmMsg` and
  `permissionRequiredMsg` into a single host confirm message.
  - cost: high (touches main-chat permission flow + tests). risk: high (out of
    step-4 scope; main-chat regression surface). reward: one confirm vocabulary.
    side-effects: scope creep beyond "/c adopts chatView". **Flag: likely
    out-of-scope.**

### G4 — Layout / View ownership (GAP-D)

`/c` stacks a scrollable turns-list ABOVE the chat, each with its own scrollbar
(context_view.go:163-191), sizing the chat band via `pane.DesiredHeight()`.
`chatView.View()` emits a FIXED `vp.Height()` rows and has no `DesiredHeight`.

- **G4-a — Add `DesiredHeight()` to `chatView`** (content-lines + status rows,
  same shape as chatpane.go:269); `regionHeights` calls it unchanged.
  - cost: med (new method computing wrapped content height from entries/viewport).
    risk: med (must agree with `vp` wrapping; off-by-one in the band split). reward:
    `/c` layout code is untouched; behavior parity. side-effects: a grow-to-content
    method the main page never uses lives on `chatView`.
- **G4-b — Fixed-split `regionHeights`** — give `/c`'s chat a fixed fraction
  (e.g. ½ or N rows) instead of grow-to-content.
  - cost: low. risk: low-med (changes `/c`'s current "pane grows with chat" feel).
    reward: no new `chatView` API; simplest. side-effects: empty chat shows a
    half-empty band (the regression `regionHeights`'s comment says it fixed,
    context_view.go:158-162). **HACK-ish** (re-opens a fixed bug).
- **G4-c — `/c` measures via `chat.TotalLineCount()`/`Height()`** and computes the
  band itself, no new `chatView` method.
  - cost: med (`/c` reimplements the pinned-row accounting `DesiredHeight` did).
    risk: med. reward: `chatView` API unchanged. side-effects: layout math leaks
    into `contextView`; duplicates logic.

Note: dual independent scrollbars already work — `chatView.View()` paints its own
scrollbar in its band; the turns-list paints its own via `renderScrollable`. The
fork is purely about how the chat BAND HEIGHT is chosen, not scrollbar painting.

### G5 — Deletion & shared types (what moves vs deletes)

`chatpane.go` (346 LOC) holds BOTH the retiring `chatPane` AND shared types other
files depend on. Inventory of what lives there:

- **Shared event types** (used by main chat + `chatView.Apply`): `ChatDriver`
  interface (chatpane.go:19), `chatStatusMsg`, `chatAssistantMsg`,
  `chatAssistantDeltaMsg`, `chatProgressMsg`, the 4 `toolEntry*Msg`,
  `permissionRequiredMsg`, `chatDoneMsg`, `chatErrorMsg`, `chatConfirmMsg`
  (chatpane.go:24-86). These MUST MOVE (delete of `chatpane.go` would break the
  whole package). Candidate homes: `chat_view.go`, or a neutral `chat_events.go`.
- **`renderChatEntry`** (chatpane.go:205) — used only by `chatPane.contentLines`.
  Deletes with the pane.
- **`chatPane` struct + methods** (Submit/Apply/View/scroll/queue/DesiredHeight,
  chatpane.go:88-346) — DELETES, modulo whatever G1/G4 ports.

References that break on delete: `context_view.go` (`pane *chatPane`,
`newChatPane`, all `cv.pane.*`), `model.go` (`cv.pane.*` in `handleContextViewKey`
and `routeChatMsg`, lines ~1707-1789), and tests
(`chatpane_test.go`, `context_view_edit_test.go:25`,
`context_view_layout_test.go:27,52`, `context_view_route_test.go:40+`).

- **G5-a — Move shared types to a new `chat_events.go`; delete `chatpane.go`
  wholesale.** Neutral home; nothing chat-component-specific.
  - cost: med (move ~60 lines + import churn). risk: low (mechanical). reward:
    clean separation; types not owned by either component. side-effects: one new
    file.
- **G5-b — Move shared types INTO `chat_view.go`; delete `chatpane.go`.**
  - cost: low-med. risk: low. reward: fewer files; events sit with their consumer.
    side-effects: `chat_view.go` (879 LOC) grows; events co-located with the
    component that is now their sole owner.
- **G5-c — Keep `chatpane.go` as an events-only file** (strip the `chatPane`
  struct, leave the types).
  - cost: low. risk: low. reward: minimal diff. side-effects: a file named
    `chatpane.go` with no `chatPane` — misleading name; **HACK** (lingering
    misnamed file). The plan says "retire chatpane.go".

### G6 — `ChatDriver` interface fit

`ChatDriver.Submit(ctx, input) tea.Cmd` is defined in chatpane.go:19 and used by
BOTH drivers. The main host already calls it directly (model.go:972 builds
`mainAgentDriver`). `/c` calls it via `chatPane.Submit`. After the pane is gone,
who calls `contextManagerDriver.Submit`?

- **G6-a — Host calls `driver.Submit` for `/c` too** (like main chat).
  `handleContextViewKey` enter-arm (model.go:1722) calls `cv.driver.Submit(...)`
  directly, then opens the placeholder per G1.
  - cost: low. risk: low. reward: symmetric with main chat; pane fully gone.
    side-effects: `contextView` must hold/expose `driver` (it already does,
    context_view.go:39).
- **G6-b — Give `chatView` a `Submit(driver, input)` convenience** mirroring the
  old pane.
  - cost: low-med. risk: low. reward: one call site. side-effects: re-adds a
    pane-ish method to `chatView`; main chat doesn't use it (asymmetry).

### G7 — Decomposition / sequencing (ship `/c` never broken mid-flight)

The retiring tests (`chatpane_test.go`) and `/c` tests must stay GREEN until the
swap, then delete/re-point in the same commit as the swap.

- **G7-a — Single atomic swap commit:** add chosen `chatView` surface (G1/G2/G4),
  re-point `contextView.pane`→`chat *chatView`, rewrite `routeChatMsg` +
  `handleContextViewKey`, delete `chatpane.go`, re-point `/c` tests, delete
  `chatpane_test.go` — all in one commit.
  - cost: high (big diff). risk: high (no intermediate green). reward: no dead
    code window. side-effects: hard to bisect.
- **G7-b — Phased:** (1) land shared-type move (G5) + new `chatView` surface
  (G1/G2/G4) behind no `/c` change — package still green, `chatPane` still used;
  (2) swap `/c` to `chatView`, re-point tests; (3) delete `chatPane` +
  `chatpane_test.go` + `renderChatEntry`.
  - cost: med (3 commits). risk: low (each commit green; `/c` works throughout).
    reward: bisectable, reviewable. side-effects: a transient window where both
    components compile.
- **G7-c — Two-phase:** combine (1)+(2), separate delete (3).
  - cost: med. risk: low-med. reward: fewer commits than G7-b. side-effects:
    larger first commit than G7-b.

---

## 3. Parity gate — prove `/c` behaves identically after the swap

`/c`'s behavior is pinned by: `context_manager_driver_test.go` (driver→event
mapping — UNAFFECTED, driver unchanged), `context_view_route_test.go` (the
`routeChatMsg` confirm/done/error/queue contract), `context_view_edit_test.go`
(rationale shows in log), `context_view_layout_test.go` (band split, scrollbar),
`queue_test.go` (FIFO + unstage + reserved rows), `context_view_*_test.go`
(scroll/refresh/rkey). `chatpane_test.go` RETIRES with the pane.

**Proposed gate:** re-point every `cv.pane.*` assertion in the `/c` tests above
onto the `chatView` surface (`cv.chat.Entries()`, `Queued()`, the new
busy/`DesiredHeight` surface chosen in G1/G4), KEEP each test's observable
assertion identical (same entry count, same rendered substrings — "removed N
turn(s).", "nothing to remove.", the rationale, the error line, the queued "⏳"
rows, the band heights), and require: (a) those re-pointed tests green, (b)
`context_manager_driver_test.go` UNCHANGED and green (driver is the contract that
must not move), (c) `go build ./... && go test ./...` clean, (d) a manual `/c`
smoke: propose an edit → confirm → delete → "removed N", and an empty proposal →
"nothing to remove", visually matching pre-swap. The driver-test-unchanged
requirement is the spine: if the `/c` event contract is preserved, the swap is
behavior-preserving by construction.

---

## Summary (controller input)

7 forks (G1 busy-state · G2 whole-msg-vs-delta · G3 confirm-unify · G4
layout/DesiredHeight · G5 type-move/delete · G6 driver.Submit caller · G7
sequencing). 4 GAPS where `chatView` lacks `chatPane`: A no-`Submit`, B no
explicit busy line, C no whole-message `Apply` arm, D no `DesiredHeight`
grow-to-content. Decisions deferred to the autonomous-run controller.
