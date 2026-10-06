# Session main-chat model override

A conversation can pin its main chat to an exact model on a saved cloud account,
without changing global routing or other conversations.

## Natural language

Ask, for example:

> For this session, use GLM 5.3 through my DeepInfra account.

The agent uses the native `session_model` tool to inspect saved profile names,
look up model IDs where the provider catalog is supported, and request the
change. Selecting or clearing an override requires confirmation, including in
bypass mode. Reading status and catalogs does not require confirmation.

The existing model finishes the current response. The selected model handles
**the next user message**. No running tool loop is transferred between models.
The requested model must actually be available on that provider; friendly names
are not guessed into API model IDs. If discovery is unavailable, supply the
provider's exact ID. Setting a route validates the saved profile and credential
configuration; remote model availability is checked when the next request runs.

## Direct terminal controls

These commands do not require a working chat model:

```text
/model                               # show override and saved profile names
/model models <profile>              # list provider model IDs, where supported
/model set <profile> <exact-model-id> # use this account/model next message
/model clear                         # restore normal routing next message
```

Wait for the command's success notice before submitting the next message.
The chat displays the selected account/model and explicitly marks that fallback
is disabled. Resuming a conversation displays its saved override as well.

## Guarantees and boundaries

- Persisted per conversation in the host-owned SQLite store; survives restart
  and resume. Conversation rollover carries the selection forward.
- Other conversations, delegated agents, and background task routing are not
  changed. Child conversations do not inherit the override.
- No profile backup chain or cross-tier fallback for pinned chat. An unavailable
  account/model produces an error rather than silently choosing another model.
  Same-provider retries may still occur under the normal retry policy.
- Local-only locus restrictions still apply. A cloud pin cannot bypass that
  privacy boundary.
- Removing or renaming the saved account makes the pin fail visibly. Use
  `/model clear` or select another account, even when chat cannot respond.
- Works with both in-process and crash-isolated worker execution. Workers get a
  per-turn snapshot of the selection; mutations are acknowledged by the host.
  Account credentials remain in the normal host credential service.

## Implementation

- `internal/chatroute`: explicit target, validation, and single-account binding.
- `internal/conversation/chat_route.go`: durable conversation setting.
- `internal/server/session_model.go`: user RPC and capability control service.
- `internal/capabilities/builtins/session_model.go`: permission-gated native tool.
- `internal/runner`: per-turn selection and fallback suppression.
- `internal/worker/session_model.go`: acknowledged host/worker control bridge.
- CLI `internal/slash/model.go` and `internal/ui/session_model.go`: direct controls
  and notices.
