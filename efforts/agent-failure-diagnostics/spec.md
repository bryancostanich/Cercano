# Agent Failure Diagnostics Log Spec

## Problem

Cercano already surfaces tool and delegation failures in the active chat, but those failures are not collected into a durable, easy-to-scan failure log. That makes post-hoc debugging hard: once the chat has moved on, we cannot quickly review what failed, which model/provider/tool was involved, whether the failure came from the parent turn or a delegated sub-agent, and what correlation identifiers connect related failures.

This is especially important for dispatch/delegation because Cercano's self-development workflow intentionally pushes read-heavy recon onto local/open models. When that path fails, the failure should be visible later without requiring the user to reconstruct the session from chat transcripts or broad routing logs.

## Goal

Add a new failure-only structured JSON Lines log dedicated to agent failures, separate from the existing routing log.

Default location:

```text
~/.config/cercano/failures.jsonl
```

The log should be optimized for later diagnostic review:

- one JSON object per failure or degraded completion;
- stable event names and failure classes;
- parent/delegation correlation fields where available;
- provider/model/tool metadata where safe;
- sanitized short error messages;
- no prompt text, tool arguments, tool outputs, API keys, or provider response bodies by default.

## Non-goals

- Do not replace normal chat-visible errors.
- Do not build a full analytics database in this slice.
- Do not log successful normal activity by default.
- Do not store raw prompts, code snippets, tool results, or model responses in this default failure log.
- Do not redesign provider routing or dispatch behavior.

## Events to log in the first slice

### Main chat / parent turn failures

Log final or meaningful degraded failures from the main agent loop, including:

- provider/model errors returned by the main loop;
- provider selection or routing errors that prevent a turn from running;
- retry/fallback exhaustion;
- tool execution failures surfaced to the loop;
- permission denial failures;
- tool-loop guard exits such as max iterations or consecutive tool-error aborts;
- context/compaction errors if they are already surfaced through the inspected paths.

### Dispatch/delegation failures

Log dispatch/workflow failures and degraded completions, including:

- dispatch argument/validation failure;
- locus/model resolution failure;
- sub-agent run failure;
- tool-loop abort inside the delegated run;
- provider/model failure inside the delegated run;
- suspicious/degraded completion when available, such as no tool usage for a read-only recon task or output truncation.

## Event schema

Each JSONL entry should include a minimal common envelope:

```json
{
  "ts": "2026-08-28T12:34:56.000000Z",
  "event": "main_chat.failed",
  "scope": "main_chat",
  "failure_class": "provider_error",
  "message": "sanitized short message",
  "conversation_id": "...",
  "turn_id": "...",
  "request_id": "...",
  "dispatch_id": "... optional ...",
  "parent_conversation_id": "... optional ...",
  "parent_turn_id": "... optional ...",
  "provider": "... optional ...",
  "model": "... optional ...",
  "tool": "... optional ...",
  "tools_granted": ["Read", "Grep"],
  "duration_ms": 1234
}
```

Exact fields can be omitted when unavailable. Use stable `snake_case` keys. Keep event names specific, for example:

- `main_chat.provider_error`
- `main_chat.tool_failed`
- `main_chat.loop_aborted`
- `main_chat.permission_denied`
- `dispatch.failed`
- `dispatch.degraded`

## Failure classes

Use a small stable enum-like vocabulary where possible:

- `provider_error`
- `provider_selection_error`
- `tool_error`
- `permission_denied`
- `tool_loop_exhausted`
- `context_error`
- `dispatch_validation_error`
- `locus_resolution_failed`
- `subagent_failed`
- `timeout`
- `panic`
- `unknown`

Provider-specific or tool-specific raw messages should be sanitized and stored in `message`; do not make raw strings into event names.

## Implementation notes from recon

- Existing structured routing log lives in `source/server/internal/routinglog/routinglog.go`, currently defaulting to `~/.config/cercano/turn-routing.jsonl`.
- Main loop orchestration and existing routing/failover events are in `source/server/internal/runner/core.go`.
- Provider resilience events are emitted from `source/server/internal/hostsvc/providers/providers.go`.
- Server and worker setup already construct and inject the routing log writer in `source/server/internal/server/server.go` and `source/server/internal/worker/worker.go`.
- Dispatch/workflow capability implementation is in `source/server/internal/hostsvc/tools/tools.go`; delegated execution goes through `source/server/internal/dispatch/engine.go`.
- Tool-loop behavior and result summaries are in `source/server/internal/agent/toolloop.go`.

## Acceptance criteria

- A new failure-only JSONL writer exists with default path `~/.config/cercano/failures.jsonl`.
- Main chat failure paths write sanitized structured failure entries.
- Dispatch/delegation failure paths write sanitized structured failure entries.
- Failure log entries include enough correlation metadata to connect dispatch failures back to a parent turn when that data is available.
- Existing chat-visible error behavior remains unchanged.
- Existing routing log behavior remains unchanged.
- Unit tests cover the writer and at least representative main-loop and dispatch failure logging paths.
- Server module tests relevant to the touched packages pass.
