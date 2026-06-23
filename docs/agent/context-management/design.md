# Context Management — Design

**Status:** Foundation (history replay) design approved. Implementation not started.

Context management for the Cercano agent: maintaining, measuring, compacting, and
curating the conversation context that gets sent to the model each turn. This doc
covers the full effort at a roadmap level, then specifies the **foundation**
(cross-turn history replay) in implementation detail. The three features that build
on it get their own sub-specs.

---

## Background: the gap that motivated this

The modern chat path (native tool calling, `RunToolLoop`) **does not carry
conversation history across turns.** Each prompt is sent to the model cold:

1. CLI sends only the new prompt — `model.go:923` `StreamChat(ctx, convID, text, wd)`;
   `text` is the single submitted line. The visible transcript (`m.entries`) is
   display-only and never sent back.
2. Client RPC carries `Input` + `ConversationId` + `WorkDir` only —
   `agentclient/client.go:705`. No history field.
3. Server passes it straight through with empty history — `server.go:825`
   `RunToolLoop{ UserInput: req.GetInput() }`, `ConvHistory` unset.
4. The loop builds context from nothing + the one message —
   `toolloop.go:112` `hist := append([]llm.Message{}, in.ConvHistory...)`.
5. `ConvHistory` is **never assigned anywhere in production** — only declared
   (`toolloop.go:38`), consumed (`:112`), and set to `nil` in a test.
6. `conversation_id` does not recover history: it drives persistence
   (`persistToolLoopTurns`) and an HTTP header (`x-opencode-session`,
   `anthropic/client.go:55`) — neither replays prior turns.

This is a regression from the legacy path (`ProcessRequest` → `loadHistory`,
`agent.go:107`), which prepended a `--- Conversation History ---` text blob. The
tool-loop migration left `ConvHistory` as the seam for replay but never filled it.

**Consequence:** the model has no memory between prompts, and the context-window
meter reads ~0 because context genuinely isn't accumulating. Everything else here
stands on fixing this.

### Why full replay is correct (not wasteful)

The Anthropic Messages API is **stateless**: every request resends the entire
`messages` array, including every `tool_use` block and its matching `tool_result`.
The model keeps no server-side memory. Claude Code, Codex, Cursor — every harness —
replays the full array each call; that is how the model "remembers" what a tool
returned. Cercano's loop already does this correctly *within* a single
`RunToolLoop` call; it just discards the array between prompts. We are making it do
across turns what it already does within a turn.

The cost of resending is addressed by (a) **prompt caching** — cache the stable
prefix server-side so the repeat is cheap (not yet implemented; separate
optimization) and (b) **compaction** — evict/summarize old turns once the window
fills (feature 4 below).

---

## Roadmap

Four features, each building on the prior. Shipped in order; each gets its own
spec → plan → implementation cycle.

| # | Feature | Depends on |
|---|---------|-----------|
| 1 | History replay (foundation) | — |
| 2 | Context meter wiring | 1 |
| 3 | `/c` context view tab | 1 |
| 4 | Background auto-compaction | 1 |

- **1 — History replay.** Persist the full tool-loop message stream losslessly;
  reload it into `ConvHistory` every turn. The model becomes conversational. **This
  doc specifies it.**
- **2 — Context meter wiring.** Make the meter reflect the assembled history's token
  count against the *cloud* model's window. (Sketched under Future Work.)
- **3 — `/c` context view tab.** A content page (sibling of `/m`) showing the
  context and letting the user chat with a context-manipulation model to
  delete/retain turns. (Sketched under Future Work.)
- **4 — Background auto-compaction.** A local model tidies/summarizes history each
  turn, per a setting; the long-term window-bounding mechanism. (Sketched under
  Future Work.)

**Bounding policy for the foundation:** none. Replay everything; rely on feature 4
to bound the window. Correctness first; short/medium conversations just work.

---

## Foundation: cross-turn history replay

### 1. Make persistence lossless — `server.go` `persistToolLoopTurns`

Today the store cannot round-trip a tool-calling turn. `persistToolLoopTurns`
(`server.go:857`) saves the initial user text and **only assistant messages**
(`:883` skips every non-assistant message). Tool results come back as **user-role
messages carrying `tool_result` blocks** (`toolloop.go:227/240/265`) and are
dropped. The store ends up with `tool_use` blocks that have no matching
`tool_result` — unreplayable, and an API hard-reject if sent.

Fix: persist the loop's **new messages faithfully**, in order.

- Each `llm.Message` → one `Turn`:
  - `Role` — "user" or "assistant" (string already used by the store).
  - `BlocksJSON` — `json.Marshal(m.Blocks)` (the existing `content_json` column).
  - `Content` — concatenated text blocks, so `/history` and `/resume` readers
    without block-aware rendering still show something meaningful.
- Persist **all** roles of the new messages, including the user-role `tool_result`
  messages.
- Remove the current split: the separate `req.Input` user-text append (`:871`) and
  the assistant-only filter (`:883`).

**Persist the delta, not the whole array — critical.** `RunToolLoop` returns
`result.History` = the injected `ConvHistory` prefix **plus** the messages added
this turn (`toolloop.go:112` seeds `hist` from `in.ConvHistory`, then appends and
returns it). Persisting all of `result.History` every turn would re-save every prior
turn — exponential duplication. So persist only the tail added this turn:

```go
newMsgs := result.History[injectedLen:] // injectedLen = len(ConvHistory) we passed in
for _, m := range newMsgs { store.Append(...) }
```

`newMsgs[0]` is the current user message (the prompt), followed by the assistant /
`tool_result` / assistant chain. So the current prompt is persisted exactly once,
here — no separate `req.Input` append, no double-store. The server captures
`injectedLen` when it builds `ConvHistory` (§2) and passes it to the persist step.

Token columns (`tokens_in`/`tokens_out`) keep their current best-effort behavior;
the meter (feature 2) owns accurate accounting.

### 2. Reconstruct & inject

A **pure, testable** converter in `internal/conversation`:

```go
// BuildLLMHistory converts stored turns into a window-valid llm.Message slice.
func BuildLLMHistory(turns []Turn) []llm.Message
```

- Map `Role` → `llm.RoleUser` / `llm.RoleAssistant`.
- If `BlocksJSON` is non-empty, unmarshal to `[]llm.Block`; else synthesize a single
  `BlockText` from `Content`.
- **Repair pairing** (safety + legacy data): drop any `tool_use` block with no
  following `tool_result`, and any `tool_result` with no preceding `tool_use`.
  Guarantees the returned array is always valid to send.
- Preserve order.

Injection — at the `RunToolLoop` call site (`server.go:825`), before the call:

```go
turns, _ := store.GetTurns(ctx, convID) // prior turns only; current not yet persisted
in.ConvHistory = conversation.BuildLLMHistory(turns)
```

`GetTurns` returns only already-persisted (prior) turns, so the current prompt is
passed once via `UserInput` and not duplicated in `ConvHistory`. Best-effort: a load
error logs and falls back to empty history (degrades to today's behavior, never
fails the turn).

### 3. Boundaries

- `BuildLLMHistory` is a pure function — no I/O, fully unit-testable (role mapping,
  block parse, pairing repair, ordering).
- The server does the thin load + inject.
- This is the **seam for feature 4**: compaction rewrites/summarizes turns before
  `BuildLLMHistory`, or post-processes its output. No other code needs to know.

### 4. Testing

- **Unit — `BuildLLMHistory`:**
  - text-only turns → text blocks, order preserved;
  - assistant `tool_use` + following user `tool_result` → round-trips intact;
  - orphan `tool_use` (legacy lossy data) → stripped, no panic;
  - orphan `tool_result` → stripped.
- **Round-trip:** persist a loop's `result.History` → `GetTurns` →
  `BuildLLMHistory` equals the original `[]llm.Message` (modulo repair).
- **Integration** (extend `toolloop_persist_test.go`):
  - after one turn completes, a second request observes non-empty, well-formed
    `ConvHistory`;
  - **no duplication** — after N turns, the turn count grows linearly (each turn
    appends only its own delta), not by re-saving prior history.

### 5. Backward compatibility

- **Existing conversations** were saved lossily (orphan `tool_use`, no
  `tool_result` turns). The §2 pairing-repair handles them: orphans are stripped,
  so old conversations replay as text + whatever paired cleanly — never crash, never
  API-reject. No data migration required.
- **`/history` and `/resume`** read the same `turns` table. They keep working
  (`Content` text preserved). The timeline now contains extra `tool_result` turns;
  block-unaware readers render their `Content` (empty for pure tool-result turns) —
  acceptable.

---

## Future work (separate sub-specs)

Sketches only — enough to show the seams. Each gets its own design doc before
implementation.

### Feature 2 — Context meter wiring

Root cause of the always-0 meter, beyond the (now-fixed) missing history:

- `persistToolLoopTurns` never touches the `contextmeter` registry — only the
  legacy `storeConversationTurn` path increments it (`agent.go`).
- The meter model is the **local** model (`main.go:197`
  `WithContextMeter(reg, cfg.LocalModel)`), but tool-loop turns run the **cloud**
  model — so the window (`ModelMax`) would be wrong even if wired.

Direction: count tokens on the assembled `ConvHistory` (the §2 injection point is
the natural hook) against the cloud model's window; update the per-conversation
`contextmeter.Counter`; the existing `GetContextUsage` RPC → CLI meter plumbing
(`model.go:383` `fetchContextUsage`, `renderContextMeter`) already works downstream.

### Feature 3 — `/c` context view tab

A `contentPage` sibling of the `/m` runtime dashboard:

- `slash/registry.go`: add `ResultOpenContextView`.
- New `slash` register fn (mirror `runtime.go`) → `/c`; register near `model.go:232`.
- `content_page.go`: add `contentPageContext contentPageID = "context"`.
- New `internal/ui/context_view.go` implementing `contentPage` (ID/SetSize/Update/
  View), built like `runtimeDashboard`.
- `model.go runSlash`: `case ResultOpenContextView: m.content = newContextView(...)`.
- Displays the context (turns + per-turn tokens) and hosts a chat with a
  context-manipulation model: "delete X but retain Y." Requires a server-side
  history-edit RPC and store mutators (`UpdateTurn`/`DeleteTurns`/`ReplaceTurns` —
  none exist today; the store is append + `UpdateRecap` only).

### Feature 4 — Background auto-compaction

Template already in the codebase: `internal/recap` — a debounced, off-request-path,
local-only summarizer (`Schedule(convID)` → local model → `store.UpdateRecap`).
Compaction follows the same shape:

- `CompleteFunc` over `localProvider.Process` (local only; zero-cost, private).
- Debounced per conversation; runs on `context.Background()` with a timeout.
- Reads turns, summarizes/evicts old ones, writes back compacted history (needs new
  store mutators — see feature 3).
- Hook it into `persistToolLoopTurns` (which today schedules nothing), gated on a
  new `config.Config` bool, toggleable live via the `UpdateConfig` RPC.
- Operates at the §3 seam, before `BuildLLMHistory`.

---

## Key file references

| Concern | Location |
|---|---|
| Tool-loop call | `server.go:825` |
| Lossy persist | `server.go:857` |
| Tool-result msgs | `toolloop.go:227/240/265` |
| History seam | `toolloop.go:38,112` |
| CLI send | `model.go:923` |
| Meter wiring | `main.go:187,197` |
| Local-bg template | `internal/recap` |
