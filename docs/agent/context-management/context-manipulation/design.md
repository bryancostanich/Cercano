# Context Manipulation (`/c` edit) — Design

**Status:** Design approved. Implementation not started.

Feature 3b of the context-management roadmap (see
`docs/agent/context-management/design.md`). Adds natural-language context
manipulation to the read-only `/c` viewer (3a): the user types an instruction
("drop the debugging tangent, keep the API design"), a model proposes which turns
to delete, the user confirms, and those turns are hard-deleted. Because the modern
tool-loop path rebuilds history from the store every turn
(`convHistory = BuildLLMHistory(GetTurns(...))`, server.go:899-902), deleting
turns shrinks the next turn's context automatically — no session reconciliation.

## Decisions (from brainstorming)

- **Hard delete** for v1 (physically remove turn rows). Soft-exclude/undo is a
  flagged future enhancement, not built now.
- **Propose → confirm** is mandatory: the model proposes a deletion set; nothing is
  deleted until the user approves. (Irreversible op driven by an imperfect picker.)
- **Local model with cloud fallback** for turn selection, with structured JSON
  output validated against the real turn IDs.
- The edit affordance lives **inside the `/c` tab**.

## Flow

```
/c input: "drop the debugging tangent, keep the API design"
   │  ProposeContextEdit(convID, instruction)            [read-only]
   ▼
server: picker model gets instruction + turn summaries (id,role,kind,preview)
        → JSON {delete_ids:[...], rationale:"..."} → validated vs real ids
   │  proposal
   ▼
/c marks proposed-delete turns + shows rationale → footer [y] delete / [n] cancel
   │  on y: DeleteConversationTurns(convID, ids)          [mutation]
   ▼
store hard-deletes rows → /c reloads → next RunToolLoop rebuilds shrunk context
```

Two RPCs (propose read-only, delete mutating) so the confirm gate sits cleanly
between them; the CLI never deletes without an explicit second call.

## 1. Store

`conversation.Store` is append-only today (`Append`, `GetTurns`, `UpdateRecap`,
whole-conversation `Delete`). Add one mutator:

```go
// DeleteTurns removes the named turns from a conversation. Unknown ids are
// ignored (idempotent). Other conversations' turns are never touched.
DeleteTurns(ctx context.Context, conversationID string, ids []string) error
```

Implementation: `DELETE FROM turns WHERE conversation_id = ? AND id IN (?,?,…)`.
Turns have a `TEXT id` primary key (`schema.sql`). Deleting a `tool_use` turn but
not its `tool_result` (or vice versa) is safe: `BuildLLMHistory`'s `repairPairing`
already strips orphaned tool blocks when assembling the sent history.

## 2. The picker — `internal/contextedit`

A small, testable package that turns an instruction + turn summaries into a
validated deletion set:

```go
type TurnSummary struct { ID, Role, Kind, Preview string }
type Proposal     struct { DeleteIDs []string; Rationale string }
type CompleteFunc  func(ctx context.Context, prompt string) (string, error)

// Propose asks the picker model which turns to delete. It tries local first,
// then cloud, parses the model's JSON, and validates delete_ids against the
// real turn-id set (hallucinated ids are dropped).
func Propose(ctx context.Context, instruction string, turns []TurnSummary, local, cloud CompleteFunc) (Proposal, error)
```

- **Prompt:** the instruction + a compact list of turns (`id  [role/kind]  preview`)
  with an instruction to respond ONLY with JSON
  `{"delete_ids": ["..."], "rationale": "one sentence"}`.
- **Local-first, cloud fallback:** call `local`; if it is nil, errors, or returns
  unparseable output, call `cloud`. If both fail/absent → error.
- **Validation:** parse JSON; keep only `delete_ids` present in the input turn-id
  set; if the result is empty after validation, return a "couldn't interpret"
  error so the CLI can ask the user to rephrase.
- Pure w.r.t. I/O — the model calls are injected `CompleteFunc`s, so the parser +
  validator are unit-testable with fakes.

## 3. Server RPCs + wiring

`source/proto/agent.proto` (regenerate stubs — verified command in the 3a plan):

```proto
rpc ProposeContextEdit      (ProposeContextEditRequest)      returns (ProposeContextEditResponse) {}
rpc DeleteConversationTurns (DeleteConversationTurnsRequest) returns (DeleteConversationTurnsResponse) {}

message ProposeContextEditRequest  { string conversation_id = 1; string instruction = 2; }
message ProposeContextEditResponse { repeated string delete_ids = 1; string rationale = 2; }
message DeleteConversationTurnsRequest  { string conversation_id = 1; repeated string turn_id = 2; }
message DeleteConversationTurnsResponse { int32 deleted = 1; }

// extend the 3a message so the CLI can address turns:
message ContextTurn { ... string id = 5; }
```

- `ContextTurn` gains `id` (3a returns role/kind/preview/est_tokens; the CLI needs
  the id to mark proposals and pass deletions). Update `contextTurnView` to set it.
- **`ProposeContextEdit` handler:** read `GetTurns`, build `[]contextedit.TurnSummary`
  (id + role + kind + preview, reusing 3a's `contextTurnView` logic), call
  `contextedit.Propose` with a local `CompleteFunc` (the `localProvider.Process`
  closure, like recap) and a cloud one (the cloud provider). Return ids + rationale.
- **`DeleteConversationTurns` handler:** validate convID, call
  `store.DeleteTurns(ctx, convID, ids)`, return `len(ids)` (or the count actually
  removed). No meter/session mutation — the next turn rebuilds from the store.
- agentclient wrappers for both.

## 4. CLI — `/c` edit affordance

The `contextView` content page (3a, read-only) gains an edit mode:

- **State:** an embedded single-line `textinput` (like `runtimeDashboard`'s catalog
  search), a `mode` (browse | editing | proposal), and a held `Proposal`.
- **Keys:**
  - browse: `e` → focus input (editing mode); scroll/`r`/`esc`/`q` as in 3a.
  - editing: typed text edits the input; `enter` → submit (async `ProposeContextEdit`
    via a `tea.Cmd`); `esc` → back to browse.
  - proposal: footer shows the rationale + `[y] delete  [n] cancel`; `y` → async
    `DeleteConversationTurns` then reload; `n`/`esc` → discard, back to browse.
- **Rendering:** in proposal mode, turns whose id is in `Proposal.DeleteIDs` render
  marked (e.g. a `✗` gutter + dim/strikethrough style); a rationale line sits above
  the footer.
- **Async routing:** a `contextEditMsg` (proposal result) and `contextDeleteMsg`
  (delete result) are top-level `tea.Msg`s type-asserted to `*contextView` in
  `model.go`, mirroring `runtimeDashboardActionMsg`.

## 5. Error / edge cases

| Case | Behavior |
|---|---|
| No local model and no cloud | proposal errors → "no model available for editing" |
| Model output unparseable / empty after validation | "couldn't interpret that — try rephrasing"; nothing deleted |
| Proposed id already gone (raced) | validated out at propose time; delete ignores unknown ids |
| Delete leaves orphan tool_use/tool_result | safe — `repairPairing` strips at send time |
| Empty conversation / no convID | edit disabled; browse-only |

## 6. Testing

- **Store:** `DeleteTurns` removes only the named ids of the given conversation;
  leaves other turns and other conversations intact; idempotent on unknown ids.
- **Picker (`contextedit.Propose`):** fake `CompleteFunc` returning JSON → correct
  ids + rationale; a hallucinated id (not in the input set) is dropped; malformed
  JSON → error; local returns error → cloud fallback used; both absent → error.
- **Server:** `ProposeContextEdit` with fake providers returns validated ids;
  `DeleteConversationTurns` deletes and returns the count; `GetConversationTurns`
  now includes `id`.
- **CLI:** construct `contextView` with a fixed snapshot + injected proposal →
  proposal mode marks the right turns and shows the rationale; `y` issues the delete
  command and reloads; `n` discards. Edit-mode key routing (`e` focuses, `enter`
  submits, `esc` cancels).

## Out of scope

Soft-exclude / undo (future); multi-turn negotiation (v1 is one instruction →
proposal → confirm; refine by re-typing); background auto-compaction (feature 4).

## Key file references

| Concern | Location |
|---|---|
| Stateless history rebuild (why delete just works) | `internal/server/server.go:899-902` |
| Pairing repair (orphan safety) | `internal/agent/history.go` (`repairPairing`) |
| Store interface (append-only today) | `internal/conversation/store.go` |
| Turn summary logic to reuse | `internal/server/context_turns.go` (`contextTurnView`) |
| Local-model background call pattern | `internal/recap` + `cmd/cercano/main.go` (recapComplete) |
| Content page + async msg pattern | `internal/ui/context_view.go`, `runtime_dashboard.go` |
| Embeddable input widget | `runtimeDashboard` catalog `textinput`; `prompt_input.go` |
