# Chat View Migration — Step 3 Design & Forks

**Status:** Inventory only. This document ENUMERATES the architectural forks and
quantifies the options. It does NOT pick winners — the controller decides each
fork against `khalkulo/workflow/design_decisions.md` (cost · risk · reward · side
effects).

**Goal of step 3** (per `design.md:59-62`): `chatView` takes ownership of the
event-driven update path; extend `ChatDriver`/`chatPaneMsg` for streaming deltas +
tool entries + rich status + confirm; write `mainAgentDriver`; the streaming state
machine moves behind the driver and `Model` becomes the thin host.

All file:line cites are `source/clients/cli/internal/ui/*.go` unless noted.

---

## 0. Where steps 1–2 left us

`chat_view.go` (563 LOC) already owns: the viewport (`vp`), entry rendering
(`renderEntry` 202-260, `renderAssistantMarkdown` 266-282), the scrollbar column
(`View` 163-198), text selection (298-563), scrollbar drag, drag-scroll, and
wheel. It is a **value field** `m.chat chatView` (`model.go:82`), built in `New`
(`model.go:257`), sized in `relayout` (`model.go:1396`).

Crucially, `chatView` does **not** own `[]*Entry`. The host holds `m.entries`
(`model.go:81`) and pushes a fresh snapshot every frame via
`m.chat.SetEntries(m.entries)` inside `refreshViewport` (`model.go:1500-1510`),
which also syncs `SetFocusedTool` + `SetTurnStatus`. So today `chatView` is a
**stateless-per-frame renderer over host-owned entries**. Step 3 is where that
boundary is renegotiated.

The reusable `chatPane` (`chatpane.go`, the `/c` surface) is the *target shape*:
it owns its own `entries` (49), `busy`/`activity`/`started` (50-52), `queued`
(53), and an `Apply(msg) tea.Cmd` event reducer (96-117) driven by a `ChatDriver`
(19-22). Step 3 brings the main chat toward that shape; step 4 retires `chatPane`.

---

## 1. Scope inventory

### 1a. State + logic that step 3 *can* move from `Model` into `chatView`

| Concern | Host location | LOC |
|---|---|---|
| Streaming state machine | `applyStreamMsg` 1148-1286 | 139 |
| Stream lifecycle | `submit` 959-1001, `streamEndMsg` 891-914 | ~75 |
| Cancel | `cancelCurrentStream` 1051-1065 | 15 |
| Channel drain | `waitForStream` 426-434, `streamTickMsg` 825-830 | ~15 |
| Pre-token anim tick | `progressAnimTickMsg` 939-954 | ~16 |
| Tool-entry helpers | `streamingTextEntry` 1331, `findToolEntry` 1305, `lastAssistantEntry` 1317, `toolEntryIndices` 1291 | ~50 |
| Tool-entry nav (fold/cycle) | key handler 651-693 | ~43 |
| Message queue | `queued` field 127; drain 907-913; `unstageLastQueued` 2227; `renderQueued` 2241; clear 1063 | ~50 |
| Turn telemetry fields | `turnStart`…`turnCloud` 113-117 | (struct) |

Streaming telemetry fields the machine writes: `turnStart/turnActivity/turnTokOut/
turnModel/turnCloud` (113-117), `tokIn/tokOut` (101), `cumIn/cumOut` (102),
`hadTurn` (118), `cloudState` (107), `lastModel` (105). Several of these feed
**host chrome** (see 1b), not just the transcript — a key constraint.

`[]*Entry` is mutated at **26 `append` sites** across `model.go` (42 `m.entries`
references total); only a subset are inside `applyStreamMsg`. The rest are slash
results (`runSlash` 1073-1143), tool-confirm outcomes (`toolConfirm` 1833-1877),
tool-result msgs (878-889), the cancel note (1061), and resume. Any move of
entry *ownership* into `chatView` must give all 26 a new append path.

### 1b. What stays host chrome (must NOT move in step 3)

Per `design.md:39-46` boundary and the live code:

- **Slash dispatch** — `runSlash` (1067-1146); appends system entries, swaps
  `m.content` pages, clears conversation.
- **Confirm GATE ownership** — `pendingConfirm *confirmRequest` (154), the
  resolver (1813+), `toolConfirm` (1833). The model routes y/n/esc to it
  (474, 494, 542, 578, 593). `chatView` may *request* a gate; it must not own it.
- **Header / bottom status bar / context meter / recap / perm chip / splash** —
  `renderStatus` (2354-2397: last-turn `tokIn/tokOut`, `cloudState`, meter, perm
  chip), `renderHeader` (2292), `renderRecap` (2264), splash (`splashShown` 79).
  Note the **footer reads `tokIn/tokOut/cloudState/hadTurn`** — telemetry the
  streaming machine writes. Moving the machine means defining how those values
  cross back to the host footer.
- **Shared multi-line input** — `m.input promptInput` (84).
- **History mode** — `inputHistory/historyIdx/historyStash` (91-93), recall
  (1005-1037). Distinct from the chat transcript.
- **`relayout`** (1360) — sizes `chatView`, owns `scrollbarTop`, computes body
  height including queued/recap rows.

The **live turn footer** (`activity · elapsed · tokens · engine`) is NOT in
`renderStatus`; it renders **inline on the pre-token placeholder** inside
`chatView.renderEntry` (chat_view.go:239-247) from the injected `turnStatus`.
That seam already exists.

---

## 2. The forks

Seven genuine forks. Each option is scored on the four `design_decisions.md`
dimensions. Concerns raised for one option are evaluated for all (symmetry).
No winner is chosen.

### F1 — Where does the streaming state machine live?

`applyStreamMsg` (1148-1286, 139 LOC) is a switch over 10 `StreamMsg` types
(`agentclient/client.go:812-821`) that mutates entries + telemetry. Question:
does the logic move behind the driver/event model, stay host-side, or split?

**Option A — Full replication behind the event model.** `mainAgentDriver`
forwards `StreamMsg`s as `chatPaneMsg`s; `chatView.Apply(event)` replicates the
139-LOC machine; host deletes `applyStreamMsg`.
- Cost: high. Port 139 LOC into `chatView`, build ~6 new event types (F2),
  write `mainAgentDriver` (~60 LOC), move tool-entry helpers (~50 LOC) and the
  channel drain (F7). Touches `chat_view.go`, `chatpane.go`, `model.go`, new
  `main_agent_driver.go`. Est. +250/−200 LOC.
- Risk: **silent** behavior drift — the machine has subtle ordering rules
  (post-tool prose opens a fresh entry: `streamingTextEntry` 1331-1338; notice
  inserted *above* the last entry: 1200; placeholder dropped when empty on
  ToolUseStart: 1228-1234). Replication can desync these without a compile error.
  Caught only by `stream_order_test.go` (`TestStreamOrderingToolBeforeFinalText`,
  `TestStreamOrderingInterleaveNoOrphans`) IF those tests are re-pointed at the
  driver path. New event tests needed.
- Reward: the end-state the roadmap names — one event model both surfaces drive;
  step 4 (`/c` adoption) becomes trivial; `Model` truly thin.
- Side effects: telemetry that the footer reads (`tokIn/tokOut/cloudState/
  hadTurn`, 1b) must be threaded back from `chatView` to host via return values
  or events; the footer can't reach into `chatView` internals.

**Option B — Host keeps the machine; `chatView` exposes entry-mutation methods.**
`applyStreamMsg` stays in `Model` but stops touching `m.entries` directly;
instead it calls `m.chat.AppendEntry`, `m.chat.OpenAssistant`, `m.chat.Tool*`,
etc. Entries move; logic stays.
- Cost: medium. Define ~8 mutation methods on `chatView`; rewrite the 26 append
  sites (not just the 10 in the machine) to call them. Est. +120/−90 LOC. No new
  event types, no driver.
- Risk: **loud** — a missing method is a compile error. Lower drift risk because
  the ordering logic is untouched; only the storage indirection changes. Existing
  `stream_order_test.go` stays valid unchanged.
- Reward: entry ownership genuinely moves (the roadmap's literal step-3 sentence:
  "`chatView` takes ownership of `entries`"), but the **driver/event model is NOT
  built** — that slips to a later step. Partial fulfillment of step 3.
- Side effects: `chatPane` keeps its own separate `Apply`; the "one event model"
  goal is deferred, so step 4 still faces a divergence. Telemetry stays host-side
  (no threading problem).

**Option C — Hybrid: entry ownership now, event model as a thin wrapper.**
Move entries into `chatView` with mutation methods (B), AND add a
`chatView.Apply(StreamMsg)` that internally calls those methods, with
`mainAgentDriver` a pass-through. The `chatPaneMsg` translation layer is added
but kept minimal (the machine logic lives in `chatView`, not in event-type
fan-out).
- Cost: medium-high. B's cost + a single `Apply` entry point (~30 LOC) + driver
  (~40 LOC). Est. +190/−150 LOC. Skips the per-event-type explosion of A.
- Risk: medium. Loud for the mutation methods; the `Apply` switch carries the
  same ordering-drift risk as A but in one place that the existing
  `stream_order_test.go` can target directly (feed `StreamMsg`s through `Apply`).
- Reward: both step-3 clauses met (ownership + event-driven) without committing
  to the full `chatPaneMsg` typed-event set; `chatView.Apply` can take
  `StreamMsg` directly and defer the `chatPaneMsg` mapping to step 4.
- Side effects: leaves a question of whether `/c` reuses `Apply(StreamMsg)` or
  the typed events — i.e. partially pre-empts F2.

> Note (hack flag): Option B without ever building the event model risks
> "ownership moved but `Model` still drives" becoming the permanent state — the
> roadmap's `mainAgentDriver` never lands. Not a hack per se, but a scope-cut the
> controller should make deliberately, not by drift.

### F2 — Reuse the existing `chatPaneMsg` set, or define a richer one?

The existing events (`chatpane.go:26-39`): `chatStatusMsg{activity}`,
`chatAssistantMsg{text}`, `chatDoneMsg{text}`, `chatErrorMsg{err}`,
`chatConfirmMsg{assistant,onYes,onNo}`. Mapped against the 10 `StreamMsg` types,
the gap is large:

| Need (StreamMsg) | Covered today? |
|---|---|
| Token append/**extend** | No — `chatAssistantMsg` appends a NEW entry |
| RouteSelected (model/cloud) | No — status carries `activity` only |
| Progress note → placeholder | Partial — `activity` only |
| Done + tokIn/Out/notice | Partial — `chatDoneMsg{text}` only |
| Error | Yes |
| ToolUseStart/Stop/ExecStart/ExecComplete | No — 4 events, none exist |
| PermissionRequired | Partial — `chatConfirmMsg` differs (F3) |

So ~6 event types are missing + 2 need enrichment.

**Option A — Extend the existing types in place.** Add `assistantDelta`, the 4
`toolEntry*` events, enrich `chatStatusMsg` (add `tokOut`, `model`, `cloud`),
enrich `chatDoneMsg` (add `tokIn/tokOut/notice`).
- Cost: ~80-120 LOC of new/edited event types + Apply arms. But **`chatPane`
  shares these types** and must still compile + behave; enriching `chatStatusMsg`/
  `chatDoneMsg` means auditing `chatPane.Apply` (96-117) and
  `contextManagerDriver` (context_manager_driver.go:41-69) for the new fields.
- Risk: loud (compile) for new types; **silent** for `chatPane` if an enriched
  field changes its rendering. Caught by `chatpane_test.go` +
  `context_manager_driver_test.go` IF they assert the affected fields.
- Reward: single event vocabulary; step 4 is a no-op on the event set.
- Side effects: couples the two surfaces during steps 3→4 — a change for main
  chat can regress `/c`.

**Option B — Define a separate richer event set for the main chat.** Main chat
gets `mainStreamMsg`-style events; `chatPane` keeps `chatPaneMsg` untouched until
step 4 unifies them.
- Cost: similar LOC, but duplicated vocabulary; two `Apply`s to maintain.
- Risk: loud; **no** `/c` regression risk (isolation). Step 4 must then
  reconcile two sets — deferred cost.
- Reward: main-chat work can't break `/c`; clean parallel development.
- Side effects: temporary duplication; the "one event model" only arrives at
  step 4 (consistent with `design.md:62-64` which retires `chatPane` then).

**Option C — Skip typed events; `chatView.Apply(StreamMsg)` directly** (pairs
with F1-C). No `chatPaneMsg` for the main chat at all; the driver hands
`StreamMsg` straight in.
- Cost: lowest event-layer cost (~0 new types); the machine reads `StreamMsg`
  as it does today.
- Risk: loud; zero `/c` coupling. But `/c` then can't share the event path —
  step 4 must either adopt `StreamMsg` in `/c` or build the mapping then.
- Reward: minimal new surface; defers the typed-event question to where both
  consumers are in view (step 4).
- Side effects: the roadmap's "extend `ChatDriver`/`chatPaneMsg`" clause is
  literally not done in step 3 — a deviation the controller must own.

### F3 — Who raises the confirm gate on `PermissionRequired`?

Today `applyStreamMsg` handles `TypePermissionRequired` (1270-1282) by calling
`toolConfirm(tc)` (1833) and setting `m.pendingConfirm` **directly** — same path
as the slash `/tool` confirm (1136-1137). The host owns `pendingConfirm` (154)
and the resolver. The existing `chatConfirmMsg` (chatpane.go:35-39) instead
carries `onYes/onNo tea.Cmd`s and is raised by the *model* on the `/c` path
(`handleContextViewKey` 1962-1964).

**Option A — `chatView.Apply` emits a `confirm` event; host raises the gate.**
The driver/Apply produces a `confirmRequest`-builder; host sets `pendingConfirm`.
- Cost: low. Reuse `toolConfirm` (1833) verbatim; `chatView` just surfaces the
  `pendingToolCall` (ToolUseID/Name/Args/Tier) up to the host.
- Risk: loud (the gate is host-owned, single writer). `confirm_test.go`
  (`TestResolveConfirmKey_*`) stays valid; the Allow/DenyToolCall RPC wiring
  (1839-1858) is untouched.
- Reward: keeps gate ownership where 1b mandates; matches the existing `/c`
  pattern (model raises, not pane).
- Side effects: a `chatView`→host return channel for the confirm request is
  needed (Apply must return more than a `tea.Cmd`, or push a host-handled msg).

**Option B — Reuse `chatConfirmMsg{onYes,onNo}` as the carrier.** The driver
emits `chatConfirmMsg`; host's existing routing raises the gate.
- Cost: low-medium. Must reconcile two shapes: `chatConfirmMsg` carries
  `tea.Cmd`s, but the main path needs `pendingToolCall` semantics + Allow/Deny
  RPC by `ToolUseID` (1839-1858). Either widen `chatConfirmMsg` or wrap the RPCs
  as `onYes/onNo` cmds.
- Risk: silent — if the wrapped `onYes` forgets the `ToolUseID` Allow path, the
  server-side tool loop hangs with no error. Caught only by an integration-style
  test that asserts `AllowToolCall` fires.
- Reward: one confirm event for both surfaces.
- Side effects: couples `chatConfirmMsg` to main-chat needs (touches `/c`).

**Option C — `chatView` owns its own gate.** Mirror `chatPane`'s pattern less —
let `chatView` hold a pending confirm and intercept keys.
- Cost: high; duplicates the resolver + key routing that host already has at
  474/494/542/578/593.
- Risk: silent key-routing bugs (two gates competing). Directly contradicts 1b
  ("confirm GATE ownership stays host"). Flag: **likely a hack** — rejected by
  the boundary table unless the controller overrides.
- Reward: `chatView` self-contained.
- Side effects: two confirm owners; high regression surface on the shared input.

### F4 — Queue, turn telemetry, and tool-nav ownership

Three sub-decisions, separable but listed together because they share the
"belongs to the transcript vs. the host chrome" question.

**F4a — Message queue** (`queued` 127, drain 907-913, `unstageLastQueued` 2227,
`renderQueued` 2241, clear 1063). `chatPane` already owns its queue (53, 120-143).
- Move to `chatView`: ~50 LOC; matches `chatPane` parity; but `renderQueued`
  draws **above the input** (host chrome region) and `unstageLastQueued` writes
  `m.input` (host) and calls `relayout` (host) — so a clean move needs the host
  to read `chatView.queued` for rendering + unstage. Risk: loud (compile) for
  the API; `queue_test.go` (`TestSubmitWhileStreamingQueues`,
  `TestUpArrowUnstagesLastQueued`, `TestQueuedLinesReserveViewportRows`) must be
  re-pointed.
- Keep host-side: 0 LOC; but then drain (907-913) must call into whatever owns
  `submit`, re-coupling. Reward of moving: step 4 inherits queuing like `chatPane`.

**F4b — Turn telemetry footer.** The *inline* footer already renders in
`chatView` from `turnStatus` (chat_view.go:239-247). The *bottom status bar*
reads `tokIn/tokOut/cloudState/hadTurn` (renderStatus 2384-2387) — host chrome.
- If F1 moves the machine, these four values must be **published back** to the
  host (event payload on `done`, or a getter). Risk: silent — a stale footer if
  the publish is missed; no test currently asserts the footer reflects the last
  turn. Reward of keeping telemetry host-side (F1-B): no threading needed.

**F4c — Tool-entry fold/nav** (key handler 651-693, helpers 1291-1338). Nav
mutates `m.focusedToolIdx` (167) + `e.Tool.Folded` and calls `refreshViewport`.
`chatView` already holds `focusedToolIdx` as a render input (chat_view.go:36, set
via `SetFocusedTool`).
- Move nav in: ~43 LOC; `chatView` would own focus + index cycling +
  `toolEntryIndices`. Loud API. `scrollback_tool_fold_test.go` re-pointed. But
  nav keys are dispatched from the host's central key switch (651) which also
  guards on `pendingConfirm`/selection — so the host must still call a
  `chatView.HandleNavKey` and the *enter-nav-mode* trigger (esc on empty input,
  696-703) straddles input (host) + transcript (chatView).
- Keep host-side: 0 LOC; `chatView` keeps just the render input it has now.

### F5 — Decomposition into shippable sub-tasks

Constraint: every sub-task builds green, keeps `stream_order_test.go` +
`queue_test.go` + `confirm_test.go` + the step-1 golden passing, and never
regresses the main page mid-flight. Proposed arc (the controller may resequence):

1. **Entry storage move.** `chatView` owns `[]*Entry`; add mutation methods;
   rewrite the 26 append sites + the machine to call them. `applyStreamMsg` still
   lives in `Model` but pokes `chatView` (F1-B mechanics) — pure refactor, no
   behavior change. Gate: full suite + golden green.
2. **Telemetry publish boundary.** Define how `tokIn/tokOut/cloudState/hadTurn`
   cross from the machine to the host footer (return payload or getter), so the
   machine can later move without breaking `renderStatus`. Gate: footer tests
   (add one) + suite.
3. **Driver + event/Apply layer.** Introduce `mainAgentDriver` + `chatView.Apply`
   (shape per F2); route `streamTickMsg` through it; the machine logic now runs
   inside `chatView`. Re-point `stream_order_test.go` at the new entry path.
   Gate: stream-order + new Apply/event tests + suite.
4. **Delete `applyStreamMsg` from `Model`.** Remove the host copy + now-dead
   helpers (`streamingTextEntry`, `findToolEntry`, etc.). Gate: build clean (loud
   if anything still references them) + suite + golden.
5. **(Optional, per F4) Queue + tool-nav move.** Relocate `queued`/nav into
   `chatView` if the controller chose to move them. Gate: queue/fold tests +
   suite.

Each step is independently revertible. Steps 1-2 are pure refactors (lowest
risk); step 3 is the behavior-bearing one (its test re-point is the gate); step 4
is deletion (compiler-enforced). This ordering means **no intermediate state has
two live copies of the machine** except within step 3, which is covered by
re-pointed tests.

### F6 — Entry storage: `chatView`-owned vs. shared host pointer (orthogonal to F1)

Even after choosing F1, *who holds the slice* is a distinct call. Today
`m.entries` is host-owned and snapshot-pushed (refreshViewport 1509).

- **Owned by `chatView`:** the 26 host append sites (slash, confirm, tool-result,
  cancel, resume) need an append API; `m.entries` reads (e.g. `lastAssistantEntry`,
  `toolEntryIndices`, resume seeding `SeedAssistantMarkdown` 282) reroute. Cost
  ~+120/−90. Risk loud (compile). Reward: matches `chatPane`; true thin host.
- **Shared pointer (host keeps slice, `chatView` holds a reference):** fewer call
  sites change; but two owners mutating one slice is **silent-aliasing-bug**
  territory (append realloc invalidates the other's view). Risk: silent, hard to
  test. Flag: **hack** unless guarded by "only one mutates."
- **Status quo (snapshot per frame):** cheapest (0 LOC) but blocks F1-A/C, since
  an event reducer can't own state it's handed fresh each frame.

### F7 — Channel drain & cmd plumbing (correlated with F1/F2)

`waitForStream` (426-434) + `streamTickMsg` (825-830) + `streamCh` (95) +
`cancelStream` (99) form the gRPC-drain loop, all host-side today.

- **Driver owns the drain:** `mainAgentDriver.Submit` returns a cmd that reads
  `StreamChat` and emits events (mirrors `contextManagerDriver.Submit`
  context_manager_driver.go:29-37). Cost ~+50/−40. Risk: cancellation
  (`cancelCurrentStream` 1051) must reach into the driver — silent hang if the
  ctx isn't cancelled on esc. `cancel_test.go` (`TestCancel_EscStopsStreaming`)
  is the catch.
- **Host keeps the drain, routes into `chatView.Apply`:** least movement; the
  host stays the cmd hub. Cost ~+15. Risk loud. But `Model` stays less thin —
  partially defeats the step-3 intent.

This fork rides on F1: F1-A/C want the driver to own the drain (so the host
deletes the loop); F1-B can leave it host-side.

---

## 3. Parity gate — proving zero behavior change

Streaming is dynamic, so the step-1 byte-identical golden (static render) is
necessary but not sufficient. Proposed multi-layer gate:

1. **Re-point existing streaming/order tests.** `stream_order_test.go`
   (`TestStreamOrderingToolBeforeFinalText` 38, `TestStreamOrderingInterleaveNoOrphans`
   64) currently feed `StreamMsg`s through `applyStreamMsg`. After the move they
   must feed the *new* entry path and assert the **same** resulting entry order +
   roles. If they pass unchanged-in-expectation, ordering parity is proven.
2. **Queue + cancel + confirm suites stay green unchanged:** `queue_test.go`,
   `cancel_test.go`, `confirm_test.go` (`TestResolveConfirmKey_*`, incl. the
   `ToolUseID` Allow/Deny variants 130/149) — these guard F3/F4a/F7.
3. **New driver/event reducer tests:** for the chosen F2 shape, a fake driver
   feeds each event into `chatView.Apply` and asserts the entry/telemetry
   mutation — mirroring `chatpane_test.go`'s style for the new arms
   (assistantDelta extends vs. appends; toolEntry lifecycle by ToolUseID;
   done publishes tokIn/tokOut/notice; permission raises the gate request).
4. **Scripted-event golden (recommended):** drive a canned `StreamMsg` script
   (token → progress → toolUseStart/Stop/ExecStart/ExecComplete → token → done,
   plus a permission-required variant) through **both** the pre-move host path
   and the post-move `chatView` path with a **frozen `turnStatus.start`** (the
   spinner/sweep at chat_view.go:245 is time-based), asserting the rendered
   transcript is byte-identical. This is the dynamic analogue of the step-1
   golden and the strongest single parity signal.
5. **Footer parity:** a new test asserting `renderStatus` shows the same
   last-turn `tokIn/tokOut` + `cloudState` after a scripted turn — guards F4b's
   telemetry-publish boundary (no current test covers this).
6. **`go build ./... && go test ./...` clean + manual smoke:** stream a real
   turn with a tool call + a permission prompt + a queued follow-up.

One-line gate: *re-pointed stream-order/queue/cancel/confirm suites + new
`chatView.Apply` event tests + a frozen-`turnStatus` scripted-event golden
asserting byte-identical transcript across the move, plus a new footer-telemetry
test.*

---

## Forks summary

- **F1** — Streaming machine location: full replication behind events (A) /
  host keeps machine + `chatView` mutation methods (B) / hybrid `Apply` (C).
- **F2** — Event model: extend shared `chatPaneMsg` (A) / separate main-chat set
  (B) / skip typed events, `Apply(StreamMsg)` (C).
- **F3** — Confirm gate: host raises via `confirm` event reusing `toolConfirm`
  (A) / reuse `chatConfirmMsg{onYes,onNo}` (B) / `chatView` owns a gate (C, hack).
- **F4** — Queue (a) / telemetry footer (b) / tool-nav (c): each moves to
  `chatView` or stays host.
- **F5** — Decomposition: 4 core sub-tasks (+1 optional) — entry-storage move →
  telemetry boundary → driver/Apply layer → delete `applyStreamMsg` (→ queue/nav).
- **F6** — Entry storage ownership: `chatView`-owned / shared pointer (hack) /
  status-quo snapshot — orthogonal to F1.
- **F7** — Channel drain: driver owns the drain / host keeps it — rides on F1.
