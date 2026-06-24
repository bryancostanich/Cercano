# Reusable Agent-Chat Pane (`chatPane`) — Design

**Status:** Design approved (shape + staging). Implementation not started.

Turns the `/c` context manipulation into a **second agent chat** with the same
UX as the main page — and, more importantly, factors that chat UX into
**reusable infrastructure** (`chatPane` + a pluggable `ChatDriver`) so any agent
conversation can host it. The context manager is the first consumer; the main
chat is migrated onto it in a later, gated phase.

## Why

The first 3b cut fires `ProposeContextEdit` as a silent one-shot RPC — no status,
no "working" feedback — so the `/c` edit feels clunk­y next to the main chat,
which shows a live status footer (`animateSpinnerGlyph` + `animateLimeSweep` →
`activity · elapsed · tokens · engine`) and streamed messages. The fix is not a
one-off status line; it is a reusable chat surface that the context manager (and
eventually the main chat) both drive.

## Staging (explicit gate)

1. **Phase 1 (this spec):** build `chatPane` + `ChatDriver`, the context-manager
   driver, and wire it into `/c`.
2. **User approves the `/c` experience.**
3. **Phase 2 (later spec, after approval):** migrate the main chat onto
   `chatPane`. Not built now — but Phase 1's interface MUST be designed to make
   it feasible (see "Main-chat capability").

## Architecture

```
            ┌──────────────── chatPane (reusable infra) ─────────────────┐
prompt bar ─┤ entries[] (messages) · status line · scroll · confirm gate │
            │   renders chatPaneMsg events with the standard chat UX      │
            └───────────────▲───────────────────────────┬────────────────┘
                            │ chatPaneMsg events         │ Submit(input)
                   ┌────────┴─────────┐         ┌────────▼─────────┐
                   │ ChatDriver iface │◄────────┤ user input text  │
                   └────────┬─────────┘
            ┌───────────────┴───────────────┐
   Phase 1: │ contextManagerDriver          │   Phase 2 (later): mainAgentDriver
            │  ProposeContextEdit / Delete  │     forwards real agent StreamMsg
            └───────────────────────────────┘
```

`chatPane` is agent-agnostic. A `ChatDriver` is injected; the pane sends it user
input and renders the `chatPaneMsg` events it emits. The pane reuses the leaf
primitives the main chat already has — entry rendering, the spinner/sweep
animation, and the `confirmRequest` gate — so the look is identical by
construction.

## 1. `chatPane` component

A self-contained type (e.g. `internal/ui/chatpane.go`, package `ui`):

- **State:** `entries []*Entry` (its own message log, reusing the existing `Entry`
  type), `driver ChatDriver`, live-status fields (`busy bool`, `activity string`,
  `startedAt time.Time`, `tokOut int`, `engine string`), `queued []string` (FIFO),
  `scrollOffset int`.
- **Rendering:** an entries view (reusing `renderEntry`-style rendering) + a status
  line while `busy` (reuse `animateSpinnerGlyph`/`animateLimeSweep`/the
  `turnStatusLine` formatter) + the queued messages rendered dimmed. Scrolls via
  the existing windowing helper.
- **Input:** `Submit(input string) tea.Cmd` — when idle, appends a user `Entry`,
  sets `busy`, returns `driver.Submit(ctx, input)`; **when busy, enqueues** the
  message (FIFO) and returns nil. See Message queuing.
- **Event handling:** `Apply(msg chatPaneMsg) tea.Cmd` mutates entries/status per
  event (below); on the terminal events (`done`/`error`) it clears `busy` and
  **drains the next queued message** (auto-submits it), continuing the tick.

## 1a. Message queuing (mirrors the main chat, `d808952`)

The pane owns the same queuing behavior the main chat just gained, so the Phase-2
migration inherits it:

- **Enqueue while busy:** `Submit` during an in-flight exchange appends to
  `queued` instead of starting a new one.
- **Auto-drain:** when an exchange ends (`done`/`error` clears `busy`), if `queued`
  is non-empty the pane pops the front and submits it — one queued item per
  completed exchange, FIFO.
- **Render:** queued messages render as dimmed rows (the pane's `renderQueued`,
  mirroring the main view's), so the user sees what's pending.
- **Unstage / clear (parity with main):** `↑` on an empty input pops the most
  recently queued message back for editing (`unstageLastQueued`); a cancel/`esc`
  drops the queue. These are part of the reusable behavior; the `/c` host wires
  them through its key handler.

## 2. `ChatDriver` + the event protocol

```go
type ChatDriver interface {
    Name() string                                  // e.g. "context manager"
    Submit(ctx context.Context, input string) tea.Cmd // emits chatPaneMsg events
}
```

`chatPaneMsg` is a tagged event the pane renders. **Designed to cover the main
chat's full UX** (so Phase 2's driver just forwards `StreamMsg`):

| Event | Meaning | Used by ctx-mgr (P1) | Used by main chat (P2) |
|---|---|---|---|
| `status{activity, tokOut, engine}` | live footer telemetry | yes (activity) | yes (all) |
| `assistantDelta{text}` | streamed assistant text | (as a single chunk) | yes (streaming) |
| `toolEntry{...}` | folded tool-call line | no | yes |
| `confirm{prompt, onYes, onNo}` | raise the confirm gate | yes (proposal) | yes (permission) |
| `done` | turn complete; clear busy | yes | yes |
| `errorEvent{err}` | surface an error message | yes | yes |

The event set mirrors the agent `StreamMsg` types (`TypeToken`,
`TypeRouteSelected`, `TypeProgress`, `TypeToolUse*`, `TypePermissionRequired`,
`TypeDone`, `TypeError`) so the Phase-2 main-agent driver is a thin forwarder.

## 3. Context-manager driver (Phase 1 consumer)

`contextManagerDriver{ agent *agentclient.Client, convID string }` implements
`ChatDriver`:

- `Submit(instruction)` returns a `tea.Cmd` that: emits
  `status{activity:"analyzing context…"}`; calls `ProposeContextEdit`; on success
  emits `assistantDelta{rationale}` + a `confirm` event whose `onYes` runs the
  delete and `onNo` cancels; on error emits `errorEvent`. The pane shows the
  animated status line throughout — the "working" feel.
- The confirm's `onYes` emits `status{activity:"removing…"}`, calls
  `DeleteConversationTurns`, then `done` ("removed N turns") and signals the `/c`
  turns list to reload. `onNo` → a "kept everything" `done`.

(The backend stays the existing two one-shot RPCs; the streaming *feel* is the
status line during the call. A future streaming context RPC could emit real
`assistantDelta`s with no pane change.)

## 4. `/c` integration

The `/c` content page hosts: the **context turns** (what's being edited; marked
`✗` while a proposal is live) **and** an embedded `chatPane` bound to a
`contextManagerDriver`. The main prompt bar (already routed to `/c` via the prior
rework) feeds `chatPane.Submit`. Layout: turns list (scrollable) above, the
manager chat log + status line below; the prompt bar is the input. The proposal's
marked turns appear in the turns list; the manager's messages + status appear in
the chat log.

## 5. Main-chat capability (Phase 2 readiness — design only)

Phase 1 must not paint the interface into a corner. Concretely: the event protocol
includes streaming deltas, tool entries, and rich status telemetry even though the
context manager uses a subset; `chatPane` owns its own `entries`/scroll/status so a
second instance (the main chat) is independent; the confirm event reuses the shared
`confirmRequest`. Phase 2 (separate spec) writes a `mainAgentDriver` that forwards
the existing `StreamChat` `StreamMsg`s as `chatPaneMsg`s and replaces the main
view's bespoke entry/status code with a `chatPane` — no `chatPane` change expected.

## Error / edge

| Case | Behavior |
|---|---|
| Propose error | `errorEvent` → error message in the pane; clear busy; no confirm |
| Delete error | `errorEvent` → error message; turns NOT reloaded; busy cleared |
| Empty proposal (nothing to remove) | `done` with a "nothing to remove" message; no confirm |
| Submit while busy | enqueued (FIFO); auto-drained when the current exchange ends |
| Confirm pending | the shared confirm gate intercepts keys (existing ordering) |

## Testing

- **`chatPane` (pure-ish, fake driver):** `Submit` appends a user entry + sets
  busy + invokes the driver; `Apply` of each event mutates state correctly
  (`status` sets activity, `assistantDelta` appends/extends an assistant entry,
  `confirm` raises the gate, `done` clears busy, `errorEvent` appends an error
  entry); rendering contains the messages + an animated status line while busy.
- **`chatPane` queuing:** `Submit` while busy enqueues (no new exchange);
  `Apply(done)`/`Apply(error)` with a non-empty queue auto-submits the next (and
  returns its cmd); queued messages render; `unstageLastQueued` pops the last back;
  cancel clears the queue.
- **`contextManagerDriver` (fake agentclient):** `Submit` emits
  status→assistantDelta(rationale)→confirm with the right ids; the confirm's
  `onYes` emits status→done and triggers the delete; error path emits
  `errorEvent`.
- **`/c` integration:** submitting via the prompt bar drives the pane (busy +
  status render); a proposal marks the turns list; confirm `y` deletes + reloads.

## Out of scope (this spec)

Main-chat migration (Phase 2, gated on approval); multi-turn refinement (the
manager remembering prior instructions and revising); a streaming context RPC.

## Key file references

| Concern | Location |
|---|---|
| Status line + animations to reuse | `internal/ui/model.go` (`renderStatus`, `turnStatusLine`, `animateSpinnerGlyph`, `animateLimeSweep`) |
| Entry type + rendering to reuse | `internal/ui/model.go` (`Entry`, `renderEntry`) |
| Reusable confirm gate | `internal/ui/model.go` (`confirmRequest`, `resolveConfirmKey`) |
| Agent stream protocol to mirror | `agentclient` `StreamMsg` types |
| `/c` page + prompt-bar routing | `internal/ui/context_view.go`, `model.go` (`handleContextViewKey`) |
| Context-edit RPCs | `agentclient.ProposeContextEdit` / `DeleteConversationTurns` |
