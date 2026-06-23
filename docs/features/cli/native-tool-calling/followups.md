# Native Tool Calling — V1 Follow-Ups

Tracks issues found and triaged after the initial 35-task implementation landed.

## Resolved

### F1: Persist tool-loop turns to the conversation store — DONE

`streamProcessRequestWithToolLoop` now dual-writes turns to the persistent store after `RunToolLoop` returns. User turn carries plain text in `Content`; every assistant message in `result.History` is appended with `BlocksJSON` (full `[]llm.Block` marshaled) and `Content` holding the concatenated text for readers without block awareness. Best-effort: store errors are logged to stderr but never surfaced. `EnsureConversation` is called first with the request's `WorkDir` as `projectDir` and the active cloud model name. A new `Agent.PersistentStore()` accessor exposes the store to the server. /history and /resume now cover tool-calling conversations.

### F2: WorkDir propagation into tool execution — DONE

The legacy `ProcessRequestStream` passes `req.WorkDir` as a string param to the coordinator/provider but does NOT thread it into the `agenttools` package — so neither path covered VS Code / Zed clients properly. V1 fix in the new path: `os.Chdir(req.WorkDir)` + deferred restore around `RunToolLoop`. Tools that read `os.Getwd()` (most filesystem tools, when called without an explicit `cwd` arg) now operate on the requested directory. Process cwd is restored when the helper returns. The legacy path remains broken for non-CLI clients — separate follow-up if/when that matters.

## Decided — defer

### Project-context injection
The legacy path injects `.cercano/context.md` content into the system prompt. The new path doesn't. Defer to a later follow-up — the model still works without it, just loses project-specific framing.

### `/tool` W/X requires `/bypass`
Documented limitation. The unary `InvokeTool` RPC can't surface a streaming confirm prompt. Workaround: `/bypass`, invoke, restore mode. Not a blocker for V1 daily use.

### Folded tool-entry expand/collapse keybind
V2 polish. Folded one-liners are informative enough for at-a-glance scanning.

### `internal/legacymodels/` cleanup
Awaiting non-Anthropic provider migration. No forcing function.
