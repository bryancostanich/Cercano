# Chat View Migration — Step 3 Implementation Plan

**Status:** Ready to execute. Decisions are LOCKED (see `docs/decisions/autonomous_2026-06-24.md` D3; quantified context in `step3-design.md`). This plan does NOT re-litigate forks — F1=A, F2=A, F3=A, F4a/b/c, F6, F7 are baked in below. Build green at every Task; no broken intermediate.

---

## Goal

Make the main chat **event-driven** behind a driver, mirroring `chatPane`/`contextManagerDriver`:
- `chatView` OWNS `[]*Entry` and the transcript state machine (`chatView.Apply(event)`).
- A new `mainAgentDriver` reads `agentclient.StreamChat` and emits agent-agnostic typed events; the host deletes its drain loop.
- The host (`Model`) becomes thin: routes events — **telemetry → footer**, **transcript → `chatView.Apply`**, **permission → host `toolConfirm` gate** — and renders chrome (header / footer / queued / recap / input / confirm gate).
- **Zero behavior change**, proven by a multi-layer parity gate.

## Architecture

```
StreamChat (gRPC)  ──▶  mainAgentDriver.Submit(ctx,input)  ──▶ tea.Cmd loop
                                                                   │ emits
                                                                   ▼
   chatStreamMsg{ev chatPaneMsg}  ──▶  Model.Update routes by event:
        ├─ telemetry (status/done payloads) ─▶ host footer fields (tokIn/tokOut/cloudState/hadTurn) + chatView.SetTurnStatus
        ├─ transcript events ───────────────▶ chatView.Apply(ev)   [owns []*Entry + machine]
        └─ permission ──────────────────────▶ host toolConfirm + pendingConfirm (verbatim)
```

- **Event model (F2=A):** the SHARED `chatPaneMsg` set in `chatpane.go` is extended **ADDITIVELY**. `chatPane` (the `/c` surface) keeps emitting only the OLD events and reading only OLD fields; additive fields default-zero there. New: `chatAssistantDeltaMsg`, 4 `toolEntry*Msg`, enriched `chatStatusMsg` (+`tokOut/model/cloud`), enriched `chatDoneMsg` (+`tokIn/tokOut/notice`).
- **Entry ownership (F6):** `chatView` holds `[]*Entry`; all ~26 host append sites call `chatView` mutation methods. `applyStreamMsg` lives in `Model` until Task 4, then deletes.
- **Telemetry (F4b):** stays host chrome. Events carry it; host reads payloads off events and pushes `turnStatus` into `chatView` for the inline placeholder.
- **Confirm (F3=A):** `PermissionRequired` is NOT a `chatView` event; the host raises the existing `toolConfirm`+`pendingConfirm` gate verbatim (incl. `AllowToolCall`/`DenyToolCall` by `ToolUseID`).
- **Queue (F4a):** state moves into `chatView`; host renders (`renderQueued`) + unstages (writes `m.input`) by reading `chatView.Queued()`.
- **Tool-nav (F4c):** moves into `chatView`; host detects esc-on-empty-input and delegates. Sequenced LAST.
- **Drain (F7):** `mainAgentDriver` owns the `StreamChat` channel drain + ctx cancel; host deletes `waitForStream`/`streamCh`/`cancelStream` plumbing in Task 3.

### chatView.Apply event surface (the settled shape)

`Apply` takes ONE agent-agnostic `chatPaneMsg` value and mutates `[]*Entry` + returns a `tea.Cmd` (for symmetry with `chatPane.Apply`; main-chat returns nil today). Transcript-only — telemetry/permission events are filtered by the host BEFORE `Apply` and never reach it.

| Event | chatView.Apply action (== old applyStreamMsg arm) |
|---|---|
| `chatStatusMsg{activity,tokOut,model,cloud}` | host-only (telemetry + `SetTurnStatus`); NOT passed to Apply |
| `chatAssistantDeltaMsg{token}` | extend open assistant entry, or open fresh below tools (`streamingTextEntry` logic); clear `Status` |
| `chatProgressMsg{note}` | set open-entry `Status`, else append system entry |
| `toolEntryStartMsg{id,name}` | close/drop placeholder, append folded in-progress `ToolEntry` |
| `toolEntryStopMsg{id,argsSummary}` | `findToolEntry(id).ArgsSummary = humanizeArgs(...)` |
| `toolEntryExecStartMsg{id}` | re-anchor `StartedAt`, status InProgress |
| `toolEntryExecCompleteMsg{id,detail,summary,isError}` | flip status ✓/⚠, set `ResultSummary` |
| `chatDoneMsg{text,tokIn,tokOut,notice}` | finalize streaming entry (`Final` fallback), insert notice entry above last; `text`/telemetry consumed by host |
| `chatErrorMsg{err}` | finalize streaming entry, append `stream error:` system entry |

`chatProgressMsg` is a NEW typed event (the old `chatStatusMsg{activity}` covered `/c`'s "working…" only; main-chat progress needs its own arm — additive, `/c` never emits it). The host derives `turnActivity` from the same events it routes.

> **Tool-arg humanization seam:** `humanizeArgs`/`humanizeResult` need `root`/`home`. `chatView` does not hold them. Decision: pass `root,home` into `newChatView` (store on `chatView`) so `Apply` is self-contained. Two new fields; set in `New`. (Not a fork — mechanical; both values are construction-time constants.)

### mainAgentDriver shape (settled)

Mirrors `contextManagerDriver`. New file `main_agent_driver.go`:

```go
type mainAgentDriver struct {
    agent  *agentclient.Client
    convID string
    workDir string
}

func (d *mainAgentDriver) Name() string { return "main agent" }

// Submit opens StreamChat and returns a cmd that emits the FIRST event plus a
// continuation that drains the channel. ctx is the cancelable turn context the
// host stores so Esc can abort. The channel + cancel travel inside the emitted
// chatStreamMsg so the host stays a pure router (no streamCh field).
func (d *mainAgentDriver) Submit(ctx context.Context, input string) (tea.Cmd, context.CancelFunc, error)
```

- `Submit` calls `StreamChat(ctx, convID, input, workDir)`; on error returns it (host appends the error entry as today). On success returns `(waitCmd, cancel, nil)`.
- The drain cmd reads the channel; each `StreamMsg` is mapped by a pure `streamMsgToEvent(sm) chatPaneMsg` (unit-testable, mirrors `proposalToMsg`) and wrapped in `chatStreamMsg{ev: ev, next: waitCmd}` so the loop re-issues itself. Channel close → `streamEndMsg{}` (kept).
- `chatStreamMsg` carries the next-read cmd so the host routes one event then re-arms the loop — same shape as the existing `streamTickMsg`+`waitForStream` pair, relocated into the driver.

## Tech Stack
- Go module `cercano/source/clients/cli`. Bubble Tea v2. Package `internal/ui`.
- Run all commands from `source/clients/cli`.

## Global Constraints
- Module path `cercano/source/clients/cli`.
- Commit messages: NEVER the word "Claude" anywhere (no Co-Authored-By trailer naming it).
- Step-1 golden (`chat_view_golden_test.go` + `testdata/chatview/*.golden`) stays **byte-identical** — no regen.
- These suites stay green (re-pointed only where noted): `stream_order_test.go` (re-point T3), `queue_test.go` (re-point T3+5), `cancel_test.go` (re-point T3), `confirm_test.go` (untouched — gate stays host), `chatpane_test.go` + `context_manager_driver_test.go` (additive must NOT regress `/c`).
- `chatpane.go` event types are SHARED: additive-only changes; `/c` behavior must not regress.
- Every Task ends green (`go build ./... && go test ./...`) and is one commit. No two live machines except inside Task 3 (gated by re-pointed tests).
- TDD per Task: write/adjust failing test → run red → implement → run green → commit.

---

## Forks for the controller

None. D3 covers F1–F7. Two mechanical seams surfaced (NOT forks): (a) `chatView` needs `root`/`home` for `humanizeArgs` — passed into `newChatView` (resolved above); (b) `chatProgressMsg` added as a distinct additive event so `chatStatusMsg` stays `/c`-compatible (resolved above). Both are direct consequences of the locked decisions, decided here. If the controller disagrees with either, flag before Task 1.

---

## Task 1 — Entry-storage move into `chatView` (pure refactor, F6)

**Outcome:** `chatView` owns `[]*Entry`; all host append/read sites go through new methods. `applyStreamMsg` still lives in `Model` but pokes `chatView`. Zero behavior change. Golden + full suite green.

### 1.1 — Failing test for the mutation API
Add `source/clients/cli/internal/ui/chat_view_entries_test.go`:
- `TestChatViewOwnsEntries`: `cv := newChatView(s,p,"","",79,20)`; assert `len(cv.Entries())==0`; `cv.AppendEntry(&Entry{Role:RoleUser,Content:"hi"})`; assert `cv.Entries()[0].Content=="hi"`.
- `TestChatViewStreamingTextEntry`: append a streaming assistant entry; assert `cv.streamingTextEntry()` returns it; append a tool entry; assert `streamingTextEntry()==nil` (closed).
- `TestChatViewFindToolEntry`: append a tool entry id `t1`; assert `cv.findToolEntry("t1")!=nil`, `cv.findToolEntry("x")==nil`, `cv.findToolEntry("")==nil`.
- `TestChatViewToolEntryIndices`: user, tool, assistant, tool → `cv.toolEntryIndices()==[]int{1,3}`.

Run red:
```
cd source/clients/cli && go test ./internal/ui/ -run TestChatView -count=1
```
Expected: compile failure (`newChatView` arity, undefined `Entries`/`AppendEntry`/`streamingTextEntry`/`findToolEntry`/`toolEntryIndices`).

### 1.2 — Implement entry storage on `chatView`
In `chat_view.go`:
- Add fields to `chatView`: `entries []*Entry`, `root string`, `home string`.
- Change `newChatView(styles, palette, vpWidth, vpHeight)` → `newChatView(styles, palette, root, home string, vpWidth, vpHeight int)`; store `root`/`home`.
- Add methods (ported verbatim from `model.go`, reparented `(m Model)`→`(c *chatView)`, `m.entries`→`c.entries`):
  - `Entries() []*Entry { return c.entries }`
  - `AppendEntry(e *Entry) { c.entries = append(c.entries, e) }`
  - `SetEntriesSlice(es []*Entry)` (for `/clear` and `applyResume` which null/replace the slice)
  - `lastAssistantEntry()`, `streamingTextEntry()`, `findToolEntry(id)`, `toolEntryIndices()` (move from `model.go:1291-1338`).
- `SetEntries(entries []*Entry)` (existing render method) → rename the render path so it reads `c.entries`: keep `SetEntries` for the render rebuild but have it operate on `c.entries`; the host stops passing a snapshot. Concretely: `func (c *chatView) rebuild()` does the current `SetEntries` body over `c.entries`; `refreshViewport` (host) calls `m.chat.rebuild()`.

### 1.3 — Rewrite the host append/read sites
In `model.go`, replace `m.entries` ownership with `m.chat`:
- Delete the `entries []*Entry` field from `Model` (line 81). `m.chat` owns it.
- Update `New` (line 257): `chat: newChatView(s, p, root, home, 80, 10)`.
- Rewrite every append site (design lists ~26; concretely):
  - `submit` (973-975): `m.chat.AppendEntry(&Entry{Role:RoleUser,...})` + assistant placeholder; error branch (985).
  - `runSlash` (1073,1101,1105,1118,1131,1139,1142): `ResultClearConversation` → `m.chat.SetEntriesSlice(nil)`; the rest → `m.chat.AppendEntry`.
  - `toolConfirm` (1837,1852,1864,1870): `m.chat.AppendEntry`.
  - `toolResultMsg` (887): `m.chat.AppendEntry`.
  - cancel note (`cancelCurrentStream` 1061): `m.chat.AppendEntry`.
  - `applyStreamMsg` (all `m.entries` mutations 1148-1283): route through `m.chat` — `m.chat.AppendEntry`, `m.chat.streamingTextEntry()`, `m.chat.findToolEntry()`, and for the two splice ops (Done notice insert 1200, ToolUseStart placeholder drop 1230) add `chatView` helpers `insertNoticeAboveLast(e *Entry)` and `dropLastEntry()`.
  - `SeedAssistantMarkdown` (283): `m.chat.AppendEntry(...)`.
  - resume seeding + `applyResume` (wherever it sets `m.entries`): `m.chat.SetEntriesSlice`.
- Reads: `lastAssistantEntry`/`streamingTextEntry`/`findToolEntry`/`toolEntryIndices` callers (`streamEndMsg` 898, key handler 654-686, progressAnimTick 945) → `m.chat.*`.
- `refreshViewport` (1500-1509): drop `SetEntries(m.entries)`; call `m.chat.rebuild()` after `SetFocusedTool`/`SetTurnStatus`.
- The tool-nav key handler (677-679) mutates `m.entries[idx].Tool.Folded` → `m.chat.Entries()[idx].Tool.Folded` (still host-driven this Task).

### 1.4 — Fix bare-Model + stream test constructors
- `stream_order_test.go:21`, `newStreamTestModel`: `chat: newChatView(theme.NewStyles(p), p, "", "", 79, 20)`; its setup appends to `m.entries` (23-26) → `m.chat.AppendEntry(...)`; the `drive` helper and assertions read `m.entries` → `m.chat.Entries()`. (Mechanical re-point; ordering expectations unchanged — proves storage move is behavior-neutral.)
- Any other test literal constructing `chatView` or reading `m.entries` (grep): `chat_view_test.go`, `resume_entries_test.go`, `scrollback_tool_*_test.go`, `cancel_test.go:23`, `queue_test.go`. Re-point reads to `m.chat.Entries()`, appends to `m.chat.AppendEntry`, constructor arity fixed.

Run green:
```
cd source/clients/cli && go test ./... -count=1
```
Expected: `ok cercano/source/clients/cli/internal/ui` and all packages. Golden test passes (byte-identical — render path unchanged, only storage indirection moved).

### 1.5 — Commit
```
git add -A && git commit -m "chatView owns []*Entry; route host append sites through mutation methods"
```
Expected: one commit on `chat-view`. (No "Claude" in message.)

---

## Task 2 — Telemetry-publish boundary + footer-parity test

**Outcome:** Define how `tokIn/tokOut/cloudState/hadTurn` reach the host footer once the machine moves; lock it with a footer test BEFORE the machine moves (Task 3). No production move yet — this Task adds the test that pins current behavior and a tiny helper the host will reuse.

### 2.1 — Failing footer-parity test
Add `source/clients/cli/internal/ui/footer_telemetry_test.go`:
- `TestFooterReflectsLastTurn`: build a stream test model; drive a scripted turn ending in `applyStreamMsg(TypeDone{TokIn:12,TokOut:34,Notice:""})`; assert `renderStatus()` contains `"last turn 12↑/34↓"` and `cloud: ok`.
- `TestFooterCloudNoneOnNotice`: drive `TypeDone{Notice:"cloud not configured"}`; assert `renderStatus()` contains `cloud:` + `NONE`.
- `TestFooterHiddenBeforeFirstTurn`: fresh model; assert `renderStatus()` does NOT contain `last turn`.

Run red (the first two should PASS already if `applyStreamMsg` is intact; this Task's red is the helper extraction below):
```
cd source/clients/cli && go test ./internal/ui/ -run TestFooter -count=1
```
If green immediately, that's fine — these are *regression pins*. Add the helper test that IS red:
- `TestApplyTelemetry`: a host method `m.applyTurnTelemetry(done chatDoneMsg)` sets `tokIn/tokOut/cumIn/cumOut/hadTurn/cloudState/lastModel` from the event. Assert it. (Red: method undefined.)

### 2.2 — Extract `applyTurnTelemetry` on `Model`
In `model.go`, add:
```go
// applyTurnTelemetry folds a done event's telemetry into the host footer
// fields. Pulled out of applyStreamMsg so the host keeps owning the footer
// after the transcript machine moves into chatView (step 3).
func (m *Model) applyTurnTelemetry(d chatDoneMsg) {
    if d.notice != "" { m.cloudState = "NONE" } else { m.cloudState = "ok" }
    m.tokIn = d.tokIn; m.tokOut = d.tokOut
    m.hadTurn = true
    m.cumIn += d.tokIn; m.cumOut += d.tokOut
    if d.model != "" { m.lastModel = d.model }
}
```
(`chatDoneMsg` gains `tokIn/tokOut/notice/model` in Task 3; for Task 2 define a thin local struct or pre-add the fields now — pre-add the fields in `chatpane.go` here so the type is ready and `/c` ignores them. Verify `chatpane_test.go`/`context_manager_driver_test.go` green.)
- Have `applyStreamMsg`'s `TypeDone` arm CALL `m.applyTurnTelemetry(chatDoneMsg{...})` instead of inline-writing the footer fields. The notice-insert + entry finalize stay in `applyStreamMsg` (they're transcript, move in T3). Behavior identical.

Run green:
```
cd source/clients/cli && go test ./... -count=1
```
Expected: all green; `chatpane_test.go` + `context_manager_driver_test.go` unaffected (new `chatDoneMsg` fields default-zero on the `/c` path).

### 2.3 — Commit
```
git add -A && git commit -m "extract applyTurnTelemetry; pin footer parity before machine move"
```

---

## Task 3 — Driver + typed events + `chatView.Apply` + queue move (the behavior-bearing Task)

**Outcome:** F2-A events added; `mainAgentDriver` written; `chatView.Apply` runs the transcript machine; host stream path routes through it; queue state moves into `chatView`. Re-point `stream_order_test.go`/`queue_test.go`/`cancel_test.go` at the new path. Gated by re-pointed tests + new Apply tests + scripted-event golden.

### 3.1 — Failing tests: events + Apply + driver mapping + scripted golden
Add `source/clients/cli/internal/ui/chat_view_apply_test.go`:
- `TestApply_DeltaExtendsOpenAssistant`: open streaming assistant; `cv.Apply(chatAssistantDeltaMsg{token:"Hi"})`; `cv.Apply(chatAssistantDeltaMsg{token:" there"})`; assert last entry `Content=="Hi there"`, `Status==""`.
- `TestApply_DeltaOpensFreshBelowTool`: assistant w/ content, then tool entry, then delta → new assistant entry appended BELOW the tool (mirrors `streamingTextEntry`).
- `TestApply_ToolEntryLifecycle`: `toolEntryStartMsg{id:"t1",name:"Bash"}` appends folded in-progress tool; `toolEntryStopMsg{id:"t1",argsSummary:...}` sets `ArgsSummary`; `toolEntryExecCompleteMsg{id:"t1",isError:false,summary:"ok"}` flips to ✓ + `ResultSummary`.
- `TestApply_DoneFinalizesAndNotice`: open assistant, `chatDoneMsg{text:"",tokIn:1,tokOut:2,notice:"cloud absent"}` → assistant `Streaming==false`, notice system entry inserted ABOVE the assistant.
- `TestApply_DoesNotTouchTelemetry`: `cv.Apply(chatDoneMsg{tokOut:99})` must NOT change any host footer field (Apply has no access — compile-level proof; assert `chatView` has no `tokIn` field via the test only constructing `chatView`).

Add `source/clients/cli/internal/ui/main_agent_driver_test.go`:
- `TestStreamMsgToEvent`: pure-map each `agentclient.StreamMsg` type → expected `chatPaneMsg` (Token→`chatAssistantDeltaMsg`, Progress→`chatProgressMsg`, RouteSelected→`chatStatusMsg{model,cloud}`, ToolUseStart→`toolEntryStartMsg`, …, Done→`chatDoneMsg{tokIn,tokOut,notice,model}`, PermissionRequired→`permissionRequiredMsg{...}`, Error→`chatErrorMsg`). Mirrors `proposalToMsg` style.

Add `source/clients/cli/internal/ui/scripted_golden_test.go`:
- `TestScriptedTurnTranscript`: build model; **freeze `turnStatus.start`** (inject a fixed `time.Time` via `SetTurnStatus`); drive a canned `StreamMsg` script (token → progress → toolUseStart/Stop/ExecStart/ExecComplete → token → done) through the **post-move** path (driver map → host route → `chatView.Apply`); capture `m.chat.rebuild()` content; assert byte-identical to a frozen `testdata/chatview/scripted_turn.golden`. Generate the golden ONCE from the pre-move path in 3.2 (capture before deleting the old route) and commit it; the test then guards the move.

Run red:
```
cd source/clients/cli && go test ./internal/ui/ -run 'TestApply_|TestStreamMsgToEvent|TestScriptedTurn' -count=1
```
Expected: compile failures (events + `Apply` + driver undefined).

### 3.2 — Add events (additive, F2-A) in `chatpane.go`
Append to `chatpane.go` (do NOT alter existing arms):
```go
type chatAssistantDeltaMsg struct{ token string }
type chatProgressMsg struct{ note string }
type toolEntryStartMsg struct{ id, name string }
type toolEntryStopMsg struct{ id, argsSummary string }
type toolEntryExecStartMsg struct{ id string }
type toolEntryExecCompleteMsg struct{ id, detail, summary string; isError bool }
type permissionRequiredMsg struct{ id, name, argsJSON, tier string } // host-routed, not Apply
```
Enrich existing (additive fields only):
```go
type chatStatusMsg struct{ activity string; tokOut int; model string; cloud bool }
type chatDoneMsg struct{ text string; tokIn, tokOut int; notice, model string }
```
`chatPane.Apply` (chatpane.go:96-117) and `contextManagerDriver` keep reading only the old fields — verify no edits needed there. Capture the scripted golden NOW (run the pre-move path once, write `testdata/chatview/scripted_turn.golden`).

### 3.3 — `chatView.Apply` + machine port
In `chat_view.go` add `func (c *chatView) Apply(msg tea.Msg) tea.Cmd` — a switch with the arms in the event-surface table, ported from `applyStreamMsg` (1148-1269 transcript logic only; NO telemetry, NO permission). Use `c.root`/`c.home` for `humanizeArgs`/`humanizeResult`. Add `insertNoticeAboveLast`/`dropLastEntry` helpers (from 3 above if not already in Task 1). `Apply` does NOT call `rebuild()` (host calls it after routing, as today via `refreshViewport`).

### 3.4 — `mainAgentDriver` + `chatStreamMsg`
New file `source/clients/cli/internal/ui/main_agent_driver.go` with the shape in Architecture. Add `streamMsgToEvent(sm agentclient.StreamMsg) chatPaneMsg` (pure). Add `type chatStreamMsg struct{ ev tea.Msg; next tea.Cmd }`. The drain cmd reads the channel, maps, wraps with the re-arm `next`. Channel close → `streamEndMsg{}`.

### 3.5 — Route the host stream path through it
In `model.go`:
- `submit` (959-1001): replace the `StreamChat`+`waitForStream` block with `cmd, cancel, err := (&mainAgentDriver{agent:m.agent, convID:m.convID, workDir:wd}).Submit(ctx, text)`. Keep `m.cancelStream = cancel`. Drop `m.streamCh`. Reset turn telemetry as today. Return `tea.Batch(cmd, progressAnimTick())`.
- Replace the `streamTickMsg` case (825-830) with a `chatStreamMsg` case:
  ```
  case chatStreamMsg:
      if !m.streaming { return m, nil }
      switch ev := msg.ev.(type) {
      case chatStatusMsg:            // telemetry only
          m.turnModel, m.turnCloud = ev.model, ev.cloud
          // turnActivity derived per-event below
      case chatDoneMsg:
          m.applyTurnTelemetry(ev)
          m.chat.Apply(ev)          // transcript finalize + notice
      case permissionRequiredMsg:
          tc := &pendingToolCall{ToolUseID:ev.id, Name:ev.name, Args:ev.argsJSON, Permission:ev.tier}
          m.pendingConfirm = toolConfirm(tc)
          m.chat.AppendEntry(&Entry{Role:RoleSystem, Content:m.renderConfirmPrompt(tc)})
      default:                       // transcript events
          m.chat.Apply(ev)
      }
      // derive turnActivity (writing/routing/running <tool>) from ev type, as old machine did
      m.refreshViewport()
      return m, msg.next
  ```
- Keep `streamEndMsg` (891-914) unchanged except `m.chat.lastAssistantEntry()` (already done T1) and drop the `m.streamCh` reference.
- Delete `waitForStream`/`streamTickMsg`/`streamCh` field. Keep `applyStreamMsg` for now (Task 4 deletes it) — it is now UNUSED by the live path but still compiled; `stream_order_test.go` re-points to the new path (below), so `applyStreamMsg` has no caller. To keep build green without unused-method errors (methods aren't flagged), leave it; Task 4 removes it.

### 3.6 — Queue move into `chatView` (F4a)
- Add `queued []string` to `chatView` + methods: `Queued() []string`, `Enqueue(s string)`, `DrainNext() (string, bool)`, `UnstageLast() (string, bool)`, `ClearQueue()` (port from `chatPane:120-143`).
- Host: `submit`'s queue-while-streaming branch → `m.chat.Enqueue`; `streamEndMsg` drain (907-912) → `m.chat.DrainNext()`; `unstageLastQueued` (2227) reads `m.chat.UnstageLast()` then writes `m.input`; `renderQueued` (2241) reads `m.chat.Queued()`; cancel clear (1063) → `m.chat.ClearQueue()`.

### 3.7 — Re-point tests
- `stream_order_test.go`: `drive` now feeds the **new path** — map each `StreamMsg` via `streamMsgToEvent` then route exactly as `chatStreamMsg` does (telemetry→host, transcript→`m.chat.Apply`, permission→host), then `m.refreshViewport()`. Assertions read `m.chat.Entries()`. **Expectations unchanged** → ordering parity proven.
- `queue_test.go`: `m.queued` → `m.chat.Queued()`; `unstageLastQueued` path via `m.chat`. Expectations unchanged.
- `cancel_test.go`: `m.cancelStream` is still the host field set from the driver — unchanged in spirit; assert esc calls it and clears it. Re-point any `m.entries` read to `m.chat.Entries()`.

Run green:
```
cd source/clients/cli && go test ./... -count=1
```
Expected: all green incl. re-pointed suites, new Apply/driver tests, scripted golden byte-identical, footer tests, step-1 golden.

### 3.8 — Commit
```
git add -A && git commit -m "event-driven main chat: mainAgentDriver + chatView.Apply + additive events + queue move"
```

---

## Task 4 — Delete host `applyStreamMsg` + dead helpers (compiler-enforced)

**Outcome:** Remove the now-unused host machine. Host is thin.

### 4.1 — Confirm nothing references the old machine
```
cd source/clients/cli && grep -rn 'applyStreamMsg\|func (m Model) findToolEntry\|func (m Model) streamingTextEntry\|func (m Model) toolEntryIndices\|func (m Model) lastAssistantEntry' internal/ui/
```
Expected: only the definitions (no callers, since T1 moved reads to `m.chat` and T3 re-pointed the stream path + tests).

### 4.2 — Delete
Remove from `model.go`: `applyStreamMsg` (1148-1286) and any of the four helpers now exclusively on `chatView` (the `(m Model)` copies at 1291-1338). Keep `Model` versions only if a host caller remains (grep result decides). Remove the dead `streamTickMsg` type if not already gone.

Run green:
```
cd source/clients/cli && go build ./... && go test ./... -count=1
```
Expected: clean build (loud if anything still referenced them) + all suites green + both goldens byte-identical.

### 4.3 — Commit
```
git add -A && git commit -m "delete host applyStreamMsg and now-dead transcript helpers"
```

---

## Task 5 — Tool-nav (fold/cycle) move into `chatView` (F4c, lowest-risk-last)

**Outcome:** `chatView` owns `focusedToolIdx` + nav; host detects esc-on-empty-input and delegates.

### 5.1 — Failing test
Add `source/clients/cli/internal/ui/tool_nav_test.go` (or extend `scrollback_tool_fold_test.go`):
- `TestChatViewNavCyclesToolEntries`: cv with user/tool/asst/tool; `cv.EnterToolNav()` focuses last tool idx; `cv.NavPrev()` moves to earlier tool; `cv.NavNext()` back; `cv.ToggleFocusedFold()` flips `Folded`; `cv.ExitToolNav()` sets focus -1.
- `TestChatViewEnterNavNoToolsNoop`: no tool entries → `EnterToolNav()` returns false, focus stays -1.

Run red:
```
cd source/clients/cli && go test ./internal/ui/ -run TestChatViewNav -count=1
```
Expected: undefined methods.

### 5.2 — Implement nav on `chatView`
Port from `model.go:651-703`: add `EnterToolNav() bool`, `NavPrev()`, `NavNext()`, `ToggleFocusedFold()`, `ExitToolNav()`, `InToolNav() bool` to `chat_view.go`, operating on `c.entries` + `c.focusedToolIdx` (already a field). Each mutator that changes render state lets the host call `refreshViewport` (return a bool "changed" or just have the host always refresh after delegating).

### 5.3 — Host delegates
In `model.go` key handler (651-703): replace the inline nav block with:
```
if m.chat.InToolNav() {
    switch {
    case key.Matches(msg, keys.NavUp):     m.chat.NavPrev();          m.refreshViewport(); return m, nil
    case key.Matches(msg, keys.NavDown):   m.chat.NavNext();          m.refreshViewport(); return m, nil
    case key.Matches(msg, keys.ToggleTool):m.chat.ToggleFocusedFold();m.refreshViewport(); return m, nil
    case key.Matches(msg, keys.Back):      m.chat.ExitToolNav();      m.refreshViewport(); return m, nil
    }
    m = m.preparePromptInput() // any other key drops nav, falls through
}
if key.Matches(msg, keys.Back) && m.input.Value() == "" {
    if m.chat.EnterToolNav() { m.refreshViewport(); return m, nil }
}
```
Delete `Model.focusedToolIdx` (167); `refreshViewport`'s `SetFocusedTool(m.focusedToolIdx)` becomes internal to `chatView` (it already holds `focusedToolIdx` — drop the `SetFocusedTool` sync or keep it as a no-op getter bridge). Re-point `runSlash`'s `ResultClearConversation` `m.focusedToolIdx = -1` → `m.chat.ExitToolNav()`.

### 5.4 — Re-point fold tests
`scrollback_tool_fold_test.go`: any `m.focusedToolIdx` read → `m.chat` nav API. Expectations unchanged.

Run green:
```
cd source/clients/cli && go test ./... -count=1
```
Expected: all green; both goldens byte-identical.

### 5.5 — Commit
```
git add -A && git commit -m "move tool-entry navigation into chatView; host delegates esc trigger"
```

---

## Parity gate (baked across Tasks)
- **Re-pointed** stream-order (T3), queue (T3+5), cancel (T3) — expectations unchanged ⇒ ordering/queue/cancel parity.
- **Untouched** confirm (`confirm_test.go`) — gate stays host (F3).
- **`/c` non-regression:** `chatpane_test.go` + `context_manager_driver_test.go` green at every Task (additive-only event changes).
- **New `chatView.Apply` event tests** (T3): each arm.
- **Frozen-`turnStatus` scripted-event golden** (T3): canned StreamMsg script through the post-move path, `turnStatus.start` frozen, byte-identical transcript vs `testdata/chatview/scripted_turn.golden`.
- **Footer-telemetry test** (T2): `renderStatus` shows correct last-turn `tokIn/tokOut` + `cloudState`.
- **Step-1 golden** byte-identical at every Task.
- Final: `go build ./... && go test ./...` clean + manual smoke (stream a turn with a tool call + permission prompt + queued follow-up).
