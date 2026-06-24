# Context View Tab (`/c`) — Design

**Status:** Design approved. Implementation not started.

Feature 3a of the context-management roadmap (see
`docs/agent/context-management/design.md`). A read-only `/c` content page in the
CLI that shows the active conversation's context — its turns, a per-turn token
estimate, and how full the model's window is. Builds on the merged history-replay
foundation (turns are now persisted losslessly) and the meter wiring (accurate
total occupancy).

Feature 3 was split during brainstorming: **3a (this doc)** is the read-only
viewer; **3b** — the interactive "delete X, retain Y" manipulation chat — is a
separate later spec, since it needs net-new store mutators and shares
history-rewrite machinery with compaction (feature 4).

## Goal

Open `/c` and see: an accurate "tokens used / window" header, and a scrollable
list of the conversation's turns with a per-turn token estimate, so the user can
see what the model is currently carrying and what is eating the window. Read-only
— no mutation, no session side effects.

## Why a new RPC

The only RPC that returns per-turn data today is `ResumeConversation`, which has a
**side effect**: it re-hydrates the server's in-memory session and rebuilds the
context meter. A viewer must not mutate state, so this feature adds a small
side-effect-free RPC. Per-turn token counts are not stored (the tool-loop persist
path leaves the token columns at 0; the provider reports usage per *request*, not
per turn), so the server computes a per-turn **estimate** with the existing
`contextmeter` tokenizer. The exact total still comes from `GetContextUsage`.

## Architecture

```
/c ─► newContextView(agent, convID) ─┬─ GetConversationTurns(convID) ─► []ContextTurn (role,kind,preview,≈tokens)
                                      └─ GetContextUsage(convID) ─────► used / max / percent
                                                  │
                                       contentPage: header meter + windowed turn list
```

Server builds display-ready summaries (preview + estimate) so the payload is small
and the client renders without parsing block JSON. The page copies the
`runtimeDashboard` pattern: synchronous gRPC load in the constructor, line-buffer
scroll windowing, `ViewPanel`-style rendering.

## 1. Server: `GetConversationTurns` RPC

`source/proto/agent.proto` — new RPC + messages (regenerate stubs after editing;
this repo regenerates `pkg/proto/*.pb.go` as a normal step):

```proto
rpc GetConversationTurns (GetConversationTurnsRequest) returns (GetConversationTurnsResponse) {}

message GetConversationTurnsRequest  { string conversation_id = 1; }
message GetConversationTurnsResponse { repeated ContextTurn turns = 1; }
message ContextTurn {
  string role       = 1; // "user" | "assistant" | "system"
  string kind       = 2; // "text" | "tool_use" | "tool_result"
  string preview    = 3; // flattened, truncated (~120 chars), display-ready
  int32  est_tokens = 4; // contextmeter tokenizer estimate for this turn
}
```

Handler (server-side, `internal/server`):
- Read `store.GetTurns(ctx, convID)` only. No meter/session mutation.
- For each `Turn`, derive `kind` from its blocks: if `BlocksJSON` decodes to blocks
  containing a `tool_use` → `tool_use`; a `tool_result` → `tool_result`; else
  `text`.
- `preview`: flatten whitespace and truncate. For text turns use `Content`; for
  tool turns synthesize a label (tool name + truncated args, or `→ result …` for
  results) since `Content` may be empty on pure tool-result turns.
- `est_tokens`: tokenize the turn's textual payload (Content, plus block text /
  tool args / result content) with `contextmeter`'s tokenizer (the same
  `cl100k` proxy used elsewhere). Best-effort; labeled `≈` in the UI.
- Empty/missing conversation → empty `turns` list, no error.

`source/server/pkg/agentclient/client.go`:

```go
type ContextTurn struct { Role, Kind, Preview string; EstTokens int }
func (c *Client) GetConversationTurns(ctx context.Context, conversationID string) ([]ContextTurn, error)
```

## 2. CLI: the `/c` content page — `internal/ui/context_view.go`

Mirrors `runtimeDashboard` (`internal/ui/runtime_dashboard.go`):

```go
type contextView struct {
    width, height int
    palette       theme.Palette
    styles        theme.Styles
    agent         *agentclient.Client
    convID        string
    snapshot      contextSnapshot
    scrollOffset  int
}
type contextSnapshot struct {
    Turns    []agentclient.ContextTurn
    TurnsErr error
    Usage    *agentclient.ContextUsage
    UsageErr error
}
func loadContextSnapshot(ag *agentclient.Client, convID string) contextSnapshot // gRPC; split out for tests
func newContextView(ag *agentclient.Client, p theme.Palette, s theme.Styles, convID string, w, h int) (*contextView, tea.Cmd)
```

Implements `contentPage` — `ID() → contentPageContext`, `SetSize` (store + clamp
scroll), `Update` (scroll keys; `r` reload; `esc`/`q` close), `View` — and
`contentPageScroller` (`ScrollBy`/`ScrollTo`/`ScrollState`) so the root model's
scrollbar drag works (model.go type-asserts content pages to the scroller).

Rendering:
- **Header:** a context-usage line — `used / max` with separators, `· NN%`, and a
  filled/empty bar, color-thresholded (e.g. amber → warn → error) consistent with
  the existing status-bar meter. On `UsageErr` show a dim "usage unavailable."
- **Turn list:** one line per turn — a role badge (color-coded: user = accent,
  assistant = primary, tool kinds = muted/info), `≈1.2k` (formatted), then the
  preview, truncated to width. Built as a `[]string` of lines and windowed by
  `scrollOffset` exactly like `runtimeDashboard.renderScrollableContent`
  (RowList isn't used — it can't scroll and is two-column).

`content_page.go`: add `contentPageContext contentPageID = "context"`.

## 3. Wiring

- `internal/slash/registry.go`: add `ResultOpenContextView` to the `ResultKind`
  const block.
- `internal/slash/contextview.go` (new): `RegisterContextView(r)` registers
  command `c` (`/context` is unrelated and taken; exact-match dispatch resolves
  `c` to this command) → `Result{Kind: ResultOpenContextView}`. Registered next to
  the other `Register*` calls in `model.go`.
- `model.go runSlash`: `case slash.ResultOpenContextView: cv, cmd := newContextView(m.agent, m.palette, m.styles, m.convID, m.width, m.height); m.content = cv; return m, cmd`.

## 4. Error / empty states

| State | Render |
|---|---|
| `convID == ""` (no conversation yet) | "no conversation yet" |
| `TurnsErr != nil` | error line in the panel; no crash |
| zero turns | "context is empty" |
| `UsageErr != nil` | header shows "usage unavailable", list still renders |

## 5. Testing

- **Server unit:** `GetConversationTurns` over an in-memory store seeded with a
  text turn, an assistant `tool_use` turn, and a user `tool_result` turn → correct
  `role`/`kind` per turn, non-empty `preview`, `est_tokens > 0`. A second
  assertion: calling it does **not** change `GetContextUsage` for the conversation
  (side-effect-free, unlike `ResumeConversation`).
- **CLI unit:** construct `contextView` from a fixed `contextSnapshot` (via the
  split-out load function, bypassing gRPC) → `View()` contains the previews and the
  exact total; `ScrollBy`/`ScrollState` window correctly over a long list; the
  empty, no-conversation, and usage-error states render their messages.
- **Slash unit:** `/c` dispatches `ResultOpenContextView`.

## Out of scope (feature 3b, later)

Any mutation: delete/edit/retain turns, the embedded chat box, store mutators
(`DeleteTurns`/`ReplaceTurns`), the NL manipulation model, and reconciling the
SQLite store with the agent's in-memory session. This tab is strictly read-only.

## Key file references

| Concern | Location |
|---|---|
| contentPage + scroller interfaces | `internal/ui/content_page.go` |
| Pattern to copy | `internal/ui/runtime_dashboard.go` |
| Slash result kinds | `internal/slash/registry.go` |
| runSlash page-open | `internal/ui/model.go` (runSlash) |
| Context usage RPC/wrapper | `agent.proto`; `agentclient/client.go` (GetContextUsage) |
| Turn store (read) | `internal/conversation/store.go` (GetTurns) |
| Tokenizer for estimates | `internal/contextmeter/tokenizer.go` |
