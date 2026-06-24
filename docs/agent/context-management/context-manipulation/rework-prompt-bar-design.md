# `/c` Edit Rework — Prompt Bar + Reusable Confirm — Design

**Status:** Design approved. Implementation not started.

Reworks the UX of feature 3b (context manipulation). The first cut (Task 4 of
`plan.md`) embedded a separate `textinput` in the `/c` page, focused with `e`.
That duplicated the composer. This rework drives `/c` edits from the **main prompt
bar**, and factors the `y`/`n` confirmation into a **reusable confirm primitive**
shared by the existing tool-permission gate, the context-edit delete, and future
agent confirmation prompts.

## Part A — Reusable confirm primitive

Today's confirm is tool-specific: `pendingConfirm *pendingToolCall` +
`resolveConfirmKey` hard-codes tool allow/deny/diff behavior (model.go:1931).
Generalize it.

```go
// confirmRequest is a generic confirmation shown above the prompt bar. Any
// feature (tool gate, context edit, future agent prompts) raises one; the model
// routes y / n / esc (and optional extra keys) to it.
type confirmRequest struct {
    prompt string                                  // rendered above the input
    onYes  func(Model) (Model, tea.Cmd)
    onNo   func(Model) (Model, tea.Cmd)
    extras map[string]func(Model) (Model, tea.Cmd) // non-resolving keys, e.g. "d" = show diff
}
```

- Model field becomes `pendingConfirm *confirmRequest`.
- `resolveConfirmKey(key)` generic: `y`→`onYes` (clear pending), `n`/`esc`/`ctrl+c`
  →`onNo` (clear pending), a key in `extras`→its handler (does NOT clear), else
  ignored.
- **Migrate the tool-permission gate** onto it: the two raise sites
  (PermissionRequired stream event ~model.go:1289; local `/tool` ~model.go:1156)
  build a `confirmRequest` whose `onYes`/`onNo` close over the existing
  `AllowToolCall`/`DenyToolCall` / `invokeToolCmd` logic and the "✓ approved" /
  "canceled" scrollback lines; `d` (show args JSON) becomes an `extras` entry.
  `renderConfirmPrompt` stays — it produces the `prompt` string.
- `pendingConfirm != nil` is already checked before content-page key routing
  (model.go:655), so a raised confirm intercepts keys over any active view.

This is a refactor with no behavior change for tool confirms (same keys, same
effects), plus a new reusable seam.

## Part B — Prompt-bar-driven `/c` edit

- **Remove** from `contextView`: the embedded `textinput`, the `mode` editing
  state, `e`-to-focus, and the `proposeCmd`/`onProposal`/edit-key handling that
  assumed an internal input. **Keep**: `applyProposal`, `cancelProposal`,
  `markedForDelete`, `renderTurn` marking, the proposal display, and the scroll
  methods.
- **Key routing** (model.go keypress handling): when the active content page is
  `*contextView` and no confirm is pending, route printable keys / backspace /
  cursor / **enter** to the **main prompt bar** (`m.input`) instead of the content
  page; keep `PgUp`/`PgDn`/`Ctrl+U`/`Ctrl+D` routed to `contextView` scrolling, and
  `esc`/`q` (on empty input) to close the page.
- **Submit:** when `/c` is active and the user presses enter with non-empty input,
  fire `ProposeContextEdit(convID, text)` (not `StreamChat`) and clear the input.
  A `contextEditProposalMsg` returns the proposal.
- **Proposal → confirm (uses Part A):** on `contextEditProposalMsg`, call
  `cv.applyProposal(p)` (marks turns `✗` + shows rationale) **and** raise a
  `confirmRequest{ prompt: rationale + " [y] delete  [n] cancel", onYes: <delete>,
  onNo: <cancel> }`. `y` runs `DeleteConversationTurns` then reloads the snapshot;
  `n`/`esc` clears the proposal. The delete result (`contextEditDeletedMsg`) is
  routed as today.
- On error proposal, surface a system/scrollback line ("couldn't interpret — try
  rephrasing"); no confirm raised.

## Flow

```
/c open → prompt bar live (chat submit suppressed while /c active)
  type "drop the debugging tangent" → enter → ProposeContextEdit
     → contextEditProposalMsg → cv.applyProposal + raise confirmRequest
        → [y] → DeleteConversationTurns → reload ; [n]/esc → cancel
  esc/q (empty bar, no confirm) → close /c → bar back to normal chat
```

## Error / edge

| Case | Behavior |
|---|---|
| `/c` open, enter with empty bar | no-op (don't propose) |
| Propose error | scrollback line; no confirm |
| Confirm pending | keys go to `resolveConfirmKey` (Part A), not the bar |
| Delete error | surface it (fixes a logged 3b minor: feedback on failure) |
| Tool-permission confirm | unchanged behavior, now via `confirmRequest` |

## Testing

- **Confirm primitive:** `resolveConfirmKey` over a `confirmRequest` — `y` runs
  `onYes` + clears; `n`/`esc` runs `onNo` + clears; an `extras` key runs its
  handler without clearing; unknown key ignored.
- **Tool-gate parity:** the migrated tool confirm still allows on `y` (calls
  AllowToolCall path), denies on `n`, shows args on `d` — assert via the existing
  confirm tests (update them to the new shape; behavior identical).
- **`/c` prompt-bar routing:** with `*contextView` active, a printable key edits
  `m.input`; enter with text issues a propose command and clears the input; enter
  with empty input is a no-op; `PgUp` scrolls the page not the input.
- **Proposal→confirm:** a `contextEditProposalMsg` marks the right turns and raises
  a `pendingConfirm`; resolving `y` issues the delete; `n` clears.

## Out of scope

Soft-exclude/undo; multi-turn negotiation; auto-compaction. The reusable
`confirmRequest` is built here but only adopted by the tool gate + context edit;
wiring it into other future agent prompts is incidental (the seam exists).

## Key file references

| Concern | Location |
|---|---|
| Existing confirm (to generalize) | `internal/ui/model.go` (`pendingConfirm`, `resolveConfirmKey:1931`, raise sites :1156/:1289) |
| Confirm render | `internal/ui/model.go` (`renderConfirmPrompt`) |
| Keypress routing (confirm vs content vs input) | `internal/ui/model.go` (~:655 confirm, content-page routing) |
| `/c` page to slim down | `internal/ui/context_view.go` |
| Chat submit (to branch for `/c`) | `internal/ui/model.go` (enter → StreamChat path) |
| Existing confirm tests | `internal/ui/confirm_test.go` |
