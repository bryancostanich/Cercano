# Autonomous Run — 2026-06-24

**Objective (handed off):** Implement the `/c` edit rework — drive context edits
from the main prompt bar (drop the embedded input/`e` mode) and factor y/n
confirmation into a reusable `confirmRequest` primitive shared by the
tool-permission gate and the context-edit delete. Spec:
`docs/agent/context-management/context-manipulation/rework-prompt-bar-{design,plan}.md`.

**Mode:** subagent-driven (implement + review + fix per task, final review).
Ranking for decisions: correctness > cleanliness > future cost.
Hard stops: design ties, irreversible actions, 5 failed fixes on one issue.
**Do NOT push. Do NOT merge to main** (await per-act authorization on return).

## Decision log

### D1 — Merge plan Tasks 2 & 3 into one atomic dispatch
- **Fork:** Plan separates "slim contextView" (T2) from "model.go prompt-bar routing" (T3). T2 removes contextView fields/methods that the first-cut model.go references, so T2 cannot compile-green alone without throwaway stubbing.
- **Options:** (a) keep split with stubs in T2, replaced in T3; (b) merge into one dispatch.
- **Chosen: (b)** merge. Ranking correctness>cleanliness: one atomic UI rewire compiles+tests as a unit and avoids a broken intermediate state; the throwaway stubbing in (a) is wasted churn and a likely error source.
- **Reversible:** yes (worktree). Not a hard-stop.

### D2 — Fix delete-error surfacing found at final review
- Final whole-branch review (opus) returned "merge with fixes": `onContextDeleted` swallowed the delete-RPC error, violating the design's "Delete error | surface it" requirement.
- **Action:** dispatched a fix (commit 88c4621) — surface the error in scrollback, skip reload on failure; also guard empty proposals from raising a confirm. Re-review clean.
- Ranking correctness>...: a stated requirement + a silent success-looking failure = must-fix before declaring done.

## OUTCOME
All review gates passed. Branch `cedit-rework` complete (4 commits: confirm refactor, decision doc, /c rewire, error-fix). NOT merged to main, NOT pushed — awaiting user's per-act merge authorization on return.

---

# Autonomous Run #2 — 2026-06-24 · chatView switchover (step 1)

## Status (end-of-run summary)

**FULLY PORTED — all 4 steps COMPLETE, merge-ready, NOT pushed, NOT merged**
(awaiting Bryan's per-act authorization). Updated objective from Bryan mid-run:
"keep going, don't merge until fully ported." Done.

- **Outcome:** The entire chatView migration is complete. There is now ONE chat
  component (`chatView`); both the main chat and `/c` drive it via their own
  `ChatDriver` (`mainAgentDriver` / `contextManagerDriver`); the thin `chatPane`
  is deleted. 27 commits on `chat-view` (base `3a4e561`); +5530/−1389 across 45
  files. `go vet`/`go build`/`go test ./...` all green. The main page's render is
  **byte-identical** throughout (step-1 golden + a scripted-event golden, both
  frozen and verified non-vacuous).
- **Each step passed its own whole-branch opus review:**
  - Step 1 (extract transcript+viewport): Ready to merge, no Critical/Important.
  - Step 2 (move scroll/selection/scrollbar-drag in): 1 Critical fixed (`ScrollbarHit`
    grabbed the gap column — PROBE fixture mismatched production geometry; `016844d`).
  - Step 3 (entry ownership + event model + `mainAgentDriver`, delete `applyStreamMsg`):
    Ready, no Critical/Important; parity proof verified genuine.
  - Step 4 (`/c` adopts `chatView`, retire `chatPane`): 1 Critical fixed (confirm-flow
    orphan placeholder — G1 refined to fill-and-close + explicit busy flag; `d77efb4`).
- **Decisions made (autonomous forks, full 7-step in the log below):** D1 (value
  field), D2 (local-coord widget boundary), D3 (F1–F7: full event-driven port,
  agent-agnostic), D4 (G1–G7: `/c` adopts `chatView`; G1 refined at review).
- **Blocked:** none. No tie, no 5-fix-loop, no irreversible action.
- **Two review-found Criticals, both fixed + regression-tested** (`ScrollbarHit`
  geometry, `/c` confirm orphan). Both were silent defects the green suite missed
  until the adversarial whole-branch reviews — the review layer earned its keep.
- **Review first (for Bryan):** (1) the two Critical fixes `016844d` + `d77efb4`
  against D2/D4-G1 in this log; (2) the parity gates — `chat_view_golden_test.go` +
  `testdata/chatview/{*.golden,scripted_turn.golden}` (byte-identical main render)
  and `context_manager_driver_test.go` (unchanged = `/c` behavior spine); (3) the
  ONE intended UX change: `/c`'s "working…" now shows as the in-transcript spinner
  (converged to the main page's UX), not a pinned status line.
- **Smoke before merge:** run `cercano`, stream a turn with a tool call + a
  permission prompt + a queued follow-up; then `/c`: propose an edit → confirm →
  "removed N", and an empty proposal → "nothing to remove".

**Objective (handed off):** Complete the chatView switchover for the main page.
Concretely: execute the approved **step 1** of the chat-view migration — extract
the main page's transcript rendering + viewport into a new `chatView` component
that `Model` delegates to, with **zero behavior change**. Done = the main page
renders via `chatView` with full passing test coverage (byte-identical golden
parity + direct `chatView` unit tests). Spec/plan:
`docs/features/cli/chat-view/{design,plan}.md` (both approved "go" before this run).

**Mode:** subagent-driven (implement + review + fix per task, final review).
Worktree `chat-view`. Ranking: correctness > cleanliness > future cost.
Hard stops: tied options after step 5, irreversible action, 5 failed fixes on one
symptom. Do NOT push. Do NOT merge to main.

## Approved decisions (made with Bryan in brainstorming — not autonomous forks)

- **Approach:** extract-and-generalize — lift the main page's real transcript code
  into `chatView`; `/c` adopts it last; retire the thin `renderChatEntry`. (design.md)
- **Step-1 boundary:** `chatView` owns entry rendering + viewport + scrollbar;
  `Model` keeps entries, the streaming state machine, mouse selection, scrollbar
  drag (those move in later steps). (design.md)
- **Selection seam:** `chatView.View(selOverlay func(string,int) string)` takes the
  host's `renderSelectionOnLine` as a callback in step 1. (plan.md)
- **Placeholder seam:** `chatView` renders the pre-token "working…" line from an
  injected `turnStatus`, not by reaching into `Model`. (plan.md)

## Decision log (7-step protocol; one entry per fork)

### D1 — Hold `chatView` on `Model` as a value field, not a nillable pointer

- **Decision point:** Task 2 extracted the transcript view but stored it as
  `chat *chatView` (pointer). Bare `Model{}` test literals (pre-existing tests
  that don't call `New`) then nil-panic on `m.chat.*`, so the implementer added
  **13 `m.chat == nil` guards** across `model.go` + `selection.go`. `chat` is
  assigned only in `New`, so every guard is dead at runtime; two (`model.go:562`,
  `:1562`) subtly fork behavior (`height := 0; if m.chat != nil {…}` vs the old
  value-type's real default). How should `Model` hold `chatView` so bare-Model
  tests don't panic without polluting production with an impossible nil path?

- **Options (4 dimensions each):**
  - **A — pointer + 13 nil-guards (as built).** Cost: 13 dead branches, 0 extra
    test edits. Risk: masks real nil bugs; 2 guards silently change a zero-Model's
    reported height. Reward: smallest diff, no future unlock. Side effect: every
    future chatView-touching path must remember the guard pattern (propagating
    debt).
  - **B — value field `chat chatView` (recommended).** Cost: field-type change +
    `New` init; **all 13 guards delete.** Risk: zero-value `chatView` must be
    safe on the guarded paths — it is: those paths call only `vp`/size methods, and
    a zero `viewport.Model` returns 0 / empty without panic (exactly the old
    `viewport` value field's property); render paths (md access) require
    construction, same as the old `md` pointer always did. Reward: restores the
    never-nil invariant the old `viewport viewport.Model` field had — the
    extraction becomes structurally faithful, production guard-free, no
    out-of-scope test churn. Side effect: simplifies all future call sites;
    matches bubbletea's value-sub-model idiom (the old `m.viewport, cmd = …Update`
    pattern).
  - **C — pointer + one lazy accessor `chatView()` that constructs on first use.**
    Cost: 1 guard, but ~13 call sites switch to the accessor. Risk: a "read" that
    mutates (hidden construction) — a mild smell; reintroduces nil risk if a
    caller forgets the accessor. Reward: production mostly clean. Side effect:
    callers must use the accessor, not the field.
  - **D — pointer, no guards, fix the bare-Model tests to construct `chat`.**
    Cost: touches pre-existing out-of-scope test files (`confirm_test`,
    `context_view_route_test`, `runtime_dashboard_test`, …). Risk: scope creep into
    unrelated files. Reward: production clean. Side effect: test-only churn that B
    avoids entirely.

- **Hacks:** A is the hack — defensive branches added because bare-Model tests
  *happen to* skip construction; it's the fewer-lines path, not the right design
  (design_decisions.md: "any approach you'd pick because it's fewer lines
  changed"). C's lazy-init (read mutates) is a milder hack.

- **Counter-cases (step 5):** For A — smallest diff and golden-green, but 13 dead
  branches + 2 behavior forks fail correctness+cleanliness. For C — centralizes to
  1 guard, but its only edge over B is "keeps the pointer," and nothing needs a
  nil `chatView`; `viewport.Model` is copy-safe by design, so B has no copy
  hazard and C's lazy-init smell is pure downside. For D — clean production, but B
  reaches the same clean production *without* touching out-of-scope files (zero
  `chatView` is valid). B dominates each alternative on correctness→cleanliness→
  future-cost.

- **Chosen: B** (value field). Unambiguously cleanest after honest counter-cases;
  per the autonomous protocol (no human at step 7), taken. Fold in the review's
  Minor at the same time: route the few direct `m.chat.vp` pokes
  (`model.go:521/814`, `selection.go:105`) through the component's `Update`/scroll
  surface (add `ScrollUp/ScrollDown` to it) so `vp` stays private.

- **Reversible:** yes (worktree, pre-merge). Not a hard-stop. Golden gate + full
  suite re-verify after the fix.

### D2 — Coordinate boundary for moving selection/scroll into `chatView` (step 2)

- **Decision point:** Mouse text-selection, grabbable scrollbar-drag, and edge
  drag-scroll live on `Model` and key off the **absolute** screen origin
  `m.scrollbarTop` (`selection.go:72-124`, model.go mouse handlers). To move them
  into `chatView`, where does screen→local coordinate translation happen, and who
  owns the interaction state (`selection`, `scrollbarDragging`, `dragScrolling`,
  `dragMouse`)?

- **Options (4 dims):**
  - **A — `chatView` is a self-contained widget in LOCAL coords (recommended).**
    `chatView` owns the selection/scrollbar-drag/drag-scroll state and does all
    hit-testing in its own (0,0 = top-left) space; it applies the selection
    overlay inside `View()` (the step-1 `selOverlay` callback is removed). The host
    keeps layout, translates screen mouse coords → local (subtract `scrollbarTop`,
    etc.), and forwards to `chatView` mouse/key methods. Cost: a thin
    translate+forward layer in the host's 4 mouse handlers; selection.go methods
    reparent `(m Model)`→`(c *chatView)`. Risk: translate math must be right —
    **loud** (the existing selection/scroll tests catch it). Reward: `chatView`
    becomes a true reusable widget with no screen-coord knowledge — the whole point
    of the migration; `/c` (step 4) reuses its interaction wholesale. Side effect:
    host mouse handlers shrink to translate+forward.
  - **B — `chatView` owns state but is told its screen origin** (`SetOrigin(top)`)
    and translates internally. Cost: similar, plus an origin field the host must
    re-sync every layout. Risk: **silent** mis-targeting if the host forgets to
    push the origin on a resize / splash toggle. Reward: same encapsulation goal.
    Side effect: origin state duplicated (host `scrollbarTop` + `chatView` copy).
  - **C — leave selection/scroll state on `Model`; only the overlay in `chatView`**
    (step-1 status quo). Cost: ~none. Risk: none new. Reward: none — **fails the
    step's objective**: selection stays `Model`-bound and cannot be reused by `/c`,
    perpetuating the duplicate-surface problem the whole migration exists to kill.
    Side effect: blocks step 4.

- **Hacks:** C is the do-nothing non-option (fails the goal). B's host-pushed
  origin is a mild smell (duplicated, sync-obligated state).

- **Counter-cases (step 5):** For B — putting translation "inside the widget"
  sounds like better encapsulation, but it forces the host to push origin updates,
  a sync obligation that's easy to miss exactly on the events that move the origin
  (resize, splash on/off); A puts translation where the layout knowledge already
  lives, so there is no sync obligation. For C — less work, but it explicitly does
  not move selection into the component, so step 4 (`/c` reuse) stays blocked;
  not viable for the objective.

- **Chosen: A** — `chatView` as a self-contained local-coord widget; host
  translates + forwards. Cleanest, no origin-sync wart, unblocks `/c` reuse.
  Unambiguous after counter-cases → taken. The step-1 `View(selOverlay)` callback
  is removed (selection now internal).

- **Reversible:** yes (worktree). Existing selection/scrollbar-drag/drag-scroll
  tests + full suite are the parity gate after the move.

- **Step-2 outcome:** D2 applied across 4 tasks (`788533e..4704354`). Whole-branch
  opus review: structurally excellent, boundary clean, dead code removed. Found
  **1 Critical** (review-fix `016844d`): `ScrollbarHit` used `localX >= vp.Width()`
  which grabbed the 1-col gap (`width-2`) in production — a silent behavior change
  vs the old `mouse.X >= width-1`. Root cause: the PROBE test fixture sized the
  chat `w-1` instead of production's `w-2`, so it validated the wrong geometry.
  Fixed to `localX >= vp.Width()+1` and corrected the fixture to production
  geometry; re-PROBED: `X=78(gap)→MISS, X=79/80→HIT` ≡ old behavior. Lesson for
  review-first: PROBE fixtures must mirror production `relayout` sizing
  (`contentW-2`). 2 Minor noted (provisional unused `Wheel`, a stray comment).

### D3 — Step-3 architecture: full event-driven port (F1–F7)

Full quantification of all 7 forks is in `docs/features/cli/chat-view/step3-design.md`
(symmetric 4-dimension scoring per fork). This entry records the DECISIONS + the
two cross-cutting constraints that resolve them, and the counter-cases.

**Cross-cutting constraints (the tie-breakers):**
1. **Objective = "fully ported"** (Bryan's handoff). Options that move entry
   ownership but never land `mainAgentDriver`/the event model (F1-B, F2-C) leave
   the migration permanently half-done — the design itself flags F1-B as "ownership
   moved but Model still drives" risking becoming the permanent state. Rejected for
   the stated objective; this is not hedging toward the easier option.
2. **Agent-agnostic invariant** (chatpane-design.md, load-bearing): the reusable
   component must NOT depend on `agentclient.StreamMsg`; agent specifics live in
   the driver. So `chatView.Apply` takes typed agent-agnostic events, NOT
   `StreamMsg` (rejects F2-C). The existing `chatPane` already honors this; the
   unified component must too.

**Decisions:**
- **F1 = A** — the streaming machine becomes event-driven, SPLIT cleanly:
  *transcript* mutations (append/extend/tool-entry lifecycle, the 139-LOC core)
  move into `chatView.Apply(event)`; *telemetry* bookkeeping
  (`tokIn/tokOut/model/cloud/activity`) stays host chrome — the host reads it off
  the events as they pass and pushes `turnStatus` to `chatView` (the existing
  step-1 seam). Counter-case (B/C, keep machine host-side): smaller + lower drift
  risk, but does not build the driver/event model the objective requires; the
  drift risk is covered loud by re-pointed `stream_order_test.go` + a scripted
  golden, so A's main downside is mitigated.
- **F2 = A** — extend the shared `chatPaneMsg` set ADDITIVELY (`assistantDelta`,
  4 `toolEntry*`, enrich `status` with `tokOut/model/cloud`, enrich `done` with
  `tokIn/tokOut/notice`). Additive → `chatPane` (which only emits the old events
  and reads only old fields) is not regressed; `chatpane_test.go` guards it.
  Counter-case (B separate set): zero /c-coupling but duplicate vocabulary step 4
  must merge — A reaches the one-event-model end state directly and the coupling
  is additive-only, so A is cleaner end state at acceptable risk. C rejected
  (agent-agnostic invariant).
- **F3 = A** — `PermissionRequired` is routed HOST-side to the existing
  `toolConfirm` + `pendingConfirm` gate (reused verbatim, incl. `AllowToolCall`/
  `DenyToolCall` by `ToolUseID`); it is NOT a `chatView` transcript event. Keeps
  the gate single-owner (boundary 1b). Counter-case (B `chatConfirmMsg{onYes,onNo}`):
  one event for both surfaces, but wrapping the `ToolUseID` Allow/Deny as cmds
  risks a SILENT server-loop hang if the Allow path is dropped — A reuses the
  proven path with no silent-failure mode. C (chatView owns gate) = flagged hack,
  rejected by the boundary.
- **F4a = move** the queue STATE into `chatView` (matches `chatPane`; rides with
  the submit/drain lifecycle that moves under F1-A). Host still RENDERS it
  (`renderQueued`, above the input = chrome) and handles unstage (writes
  `m.input`) by reading `chatView.Queued()` — same state-in-component / chrome-in-host
  split as the selection notice.
- **F4b = telemetry stays host** — the enriched `status`/`done` events carry
  `tokOut/model/cloud/tokIn`; the host reads them for the bottom status bar +
  pushes `turnStatus` to `chatView` for the inline placeholder. No reach-in.
- **F4c = move tool-nav** (fold/cycle) into `chatView` (it owns entries +
  `focusedToolIdx`); host detects the trigger (esc on empty input) and delegates,
  mirroring the selection pattern. Sequenced LAST (lowest-risk-last).
- **F5 = decomposition** (the design's arc, F4 folded in): (1) entry-storage move
  into `chatView` + mutation methods, host machine still drives — pure refactor;
  (2) telemetry-publish boundary; (3) `mainAgentDriver` + `chatView.Apply` typed
  events + queue move, re-point stream-order tests; (4) delete host
  `applyStreamMsg`; (5) tool-nav move. Each builds green, no two-live-machines
  except within (3) which the re-pointed tests gate.
- **F6 = `chatView`-owned entries.** Shared-pointer = flagged silent-aliasing
  hack (rejected); status-quo snapshot blocks the event model. Owned is the only
  option compatible with F1-A.
- **F7 = driver owns the drain.** `mainAgentDriver.Submit` reads `StreamChat` and
  emits events (mirrors `contextManagerDriver`); host deletes its drain loop. Esc
  cancel must cancel the driver ctx — guarded loud by `cancel_test.go`.

**No tie / no BLOCK.** The objective + the agent-agnostic invariant make the "full
event model" path the clear correctness winner; counter-cases are genuinely
weaker for the stated goal. **Parity gate:** re-pointed stream-order/queue/cancel/
confirm suites + new `chatView.Apply` event tests + a frozen-`turnStatus`
scripted-event golden (byte-identical transcript across the move) + a new
footer-telemetry test. Reversible (worktree).

- **Step-3 outcome:** D3 applied across 5 tasks (`8c7730c..d167b51`). Main chat
  fully event-driven: `chatView` owns entries + transcript machine
  (`chatView.Apply`) + queue + tool-nav; `mainAgentDriver` owns the StreamChat
  drain; host is a thin router; the 139-LOC `applyStreamMsg` deleted. Whole-branch
  opus review: **Ready to proceed — no Critical/Important.** Parity proof verified
  genuine (scripted golden was asserted byte-identical to the old machine in
  commit `64503b4` before `applyStreamMsg` was deleted; frozen + unchanged at head
  → provably equal). Agent-agnostic invariant holds (`chat_view.go` has no
  `agentclient` import; the StreamMsg→event map lives in the driver). Additive
  events don't regress `/c`. 1 Minor (stale "both paths" comment in
  `scripted_golden_test.go`) — fixed.

### D4 — Step-4: `/c` adopts `chatView`, retire `chatPane` (G1–G7)

Full quantification in `docs/features/cli/chat-view/step4-design.md`. Cross-cutting
principle: the migration thesis (chatpane-design: "`/c` becomes a second agent chat
with the **same UX as the main page**") makes UX *convergence* the correct outcome,
not a regression; "fully ported" = `chatPane` deleted, one component.

- **G1 = a** — `/c`'s "working…" busy state renders via the SAME streaming-placeholder
  path the main chat uses (host opens a `Streaming` placeholder entry on submit,
  fed by `turnStatus`). One busy concept. **Consequence to flag for Bryan:** `/c`'s
  busy indicator changes from a pinned bottom status line to the in-transcript
  spinner+lime-sweep placeholder — this is the intended convergence to "same UX as
  the main page," not a bug. Counter-cases: G1-b (explicit `busy`+pinned line in
  chatView) re-introduces the dual busy concept the migration deletes (HACK-ish,
  un-unifies); G1-c (busy flag in contextView) leaves two render owners in `/c`.
  Both preserve `/c`'s divergent UX, contradicting the unification goal — weaker.
- **G2 = a** — add a `chatAssistantMsg` (whole-append) arm to `chatView.Apply`; the
  `/c` host emits it for the confirm rationale (replacing `appendAssistant`), giving
  the previously test-only event a production emitter. Unified component handles
  whole-append AND delta-extend. Counter-case G2-c (drop the event, fold into the
  shared `chatDoneMsg` arm): removes a dead event but mutates the SHARED done arm
  → main-chat regression risk; G2-a avoids touching main chat. G2-b (force deltas
  on a non-streaming driver) = wrong mental model.
- **G3 = a** — keep `/c` on `chatConfirmMsg` (host-routed to `pendingConfirm`); main
  keeps `permissionRequiredMsg`. Both gates host-owned; coexisting confirm shapes is
  already the state. G3-b (unify the confirm events) = out of step-4 scope, main-chat
  regression surface — rejected.
- **G4 = a** — add `DesiredHeight()` to `chatView` (content-lines + status rows, same
  shape `chatPane` had); `/c`'s `regionHeights` calls it unchanged → band-split parity.
  G4-b (fixed split) re-opens the "empty pane eats the panel" bug `regionHeights` fixed
  (HACK); G4-c leaks layout math into `contextView` (duplication).
- **G5 = a** — move the SHARED event types + `ChatDriver` to a new neutral
  `chat_events.go`; delete `chatpane.go` wholesale. G5-b (into `chat_view.go`, already
  879 LOC) bloats it; G5-c (events-only `chatpane.go`) leaves a misnamed file = HACK,
  contradicts "retire chatpane.go".
- **G6 = a** — the host calls `contextManagerDriver.Submit` for `/c` (symmetric with
  how the main host calls `mainAgentDriver`); no `chatView.Submit`. G6-b re-adds a
  pane-ish method only `/c` would use (asymmetry).
- **G7 = b** — phased 3 sub-tasks (each builds green, `/c` works throughout):
  (1) add the `chatView` surface (chatAssistantMsg arm, `DesiredHeight`, placeholder
  busy support) + move shared types to `chat_events.go` — `chatPane` still used;
  (2) swap `contextView` onto `chatView` + host wiring, re-point `/c` tests;
  (3) delete `chatPane` + `chatpane_test.go` + `renderChatEntry`. G7-a (atomic) has
  no intermediate green (hard to bisect a large behavior-bearing swap).

**No tie / no BLOCK.** Parity gate: re-point `/c` tests' `cv.pane.*` → `chatView`
surface keeping observable assertions identical (entry counts, "removed N turn(s)."/
"nothing to remove."/rationale/error/queued rows, band heights); `context_manager_driver_test.go`
stays UNCHANGED + green (the driver event contract is the behavior-preservation spine);
step-1 + scripted goldens byte-identical; manual `/c` smoke. Reversible (worktree).

- **Step-4 outcome + G1 refinement (review-fix):** D4 applied across 3 tasks
  (`a6a3fef..ccb06c7`); `chatPane` fully retired (424 LOC), grep-clean, goldens
  byte-identical, driver-test spine unchanged. Whole-branch opus review found **1
  Critical**: G1-a's "busy = last entry is streaming" assumption HOLDS for the main
  chat (continuous stream → done closes the open entry) but BREAKS for `/c`'s
  *confirm-gated* flow — the host appended the rationale entry AFTER the open
  placeholder on `chatConfirmMsg`, so the placeholder was never last, never closed
  → a frozen `working…` spinner orphaned on every successful context edit, and
  `busy()` wrongly read false during the confirm gate (auto-refresh no longer
  suppressed). **G1 refined:** the placeholder stays the propose-phase VISUAL, but
  (a) `chatConfirmMsg` FILLS-and-closes the open placeholder with the rationale
  (mirrors the `chatDoneMsg` fill semantics) instead of appending, and (b) `/c`
  busy STATE is an explicit `contextView` flag (set on Submit, cleared on
  done/error) — NOT derived from the placeholder. (This grafts G1-c's explicit
  state flag onto G1-a's visual; pure G1-a was insufficient for a gated single-shot
  flow — a real gap the design's quantification missed.) Plus a lifecycle test
  (submit→confirm→done leaves zero `{streaming,empty}` entries; no `working…` in the
  rendered `/c` view after completion) — the regression slipped because nothing
  asserted placeholder lifecycle on the confirm path.

- **G1 fix applied (2026-06-25):** 3 files changed, new test file added.
  `chat_view.go`: `FillOpenAssistant(text string) bool` — fills+closes the open
  streaming entry, returns false if none open (host falls back to append).
  `context_view.go`: `busyFlag bool` field added; `busy()` reads it instead of
  `streamingTextEntry()`.
  `model.go`: `submitContextEdit` sets `cv.busyFlag = true`; `routeChatMsg`
  `chatConfirmMsg` arm calls `cv.chat.FillOpenAssistant(cm.assistant)` (not append);
  `onNo` arm sets `cv.busyFlag = false` directly; general arm clears `busyFlag` on
  `chatDoneMsg`/`chatErrorMsg` before `Apply`.
  `context_view_busy_test.go`: 2 lifecycle tests (submit→confirm-yes→done and
  submit→confirm-no) asserting (a) zero `{Streaming:true,Content==""}` entries,
  (b) no `working…` in rendered view, (c) rationale/done text present, (d) busy
  spans the gate. Build clean, vet clean, `go test ./... -count=1` green (all 6
  packages), goldens byte-identical, driver test unchanged.
