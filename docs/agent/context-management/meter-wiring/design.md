# Context Meter Wiring — Design

**Status:** Design approved. Implementation not started.

Feature 2 of the context-management roadmap (see
`docs/agent/context-management/design.md`). Builds on the merged history-replay
foundation. Makes the CLI context-window meter show **real** occupancy for the
cloud tool-loop path: actual provider-counted tokens against the cloud model's
context window. Today the meter reads ~0 because nothing on the tool-loop path
updates the counter, and the window it would use is the wrong (local) model's.

## Goal

After each cloud chat turn, the meter reflects how full the model's context
window is, using the **provider-reported** token count (which includes the system
prompt and tool-definition schemas, not just the message text) measured against
the **cloud** model's window.

## Background: why it reads 0

- The per-conversation token counter (`contextmeter.Registry`/`Counter`) is only
  updated by the **legacy** path (`agent.go:149` `storeConversationTurn`, snapshot
  semantics). The modern tool-loop path never touches it.
- `WithContextMeter(reg, cfg.LocalModel)` (`agent.go:46`, wired in
  `cmd/cercano/main.go`) sets the meter's max-window model to the **local** model,
  but tool-loop turns run the **cloud** model — so even the window size is wrong.
- Provider token usage is dropped on the streaming path: `ChatResponse` has
  `InputTokens`/`OutputTokens` (`internal/llm/provider.go:24-27`) and the
  non-streaming `Chat` fills them (`anthropic/client.go:120`), but the tool loop
  uses **streaming**, and `llm.StreamEvent` (`internal/llm/stream.go`) carries no
  usage. `collectStream` (in `toolloop.go`) rebuilds only blocks + stop reason.

The downstream plumbing is intact and needs no change: `GetContextUsage` RPC →
`agent.GetContextUsage` (`agent.go:270`) → CLI `fetchContextUsage` /
`renderContextMeter`, already polled once per turn after `streamEndMsg`.

## Approach

Thread provider usage from the streaming events through to the loop result, then
snapshot the meter against the cloud window after each turn. Snapshot semantics
(reset-then-add) match the existing legacy path.

```
message_start.usage ─┐
                     ├─► StreamEvent ─► collectStream ─► ChatResponse(.In/.Out)
message_delta.usage ─┘                                          │
                                                                ▼
                         last loop call ─► ToolLoopResult(.In/.Out)
                                                                │
              server (cloud model) ─► agent.RecordContextUsage ─┘
                                          │ Get(conv,model); SetMax; Reset; AddCount
                                          ▼
                              contextmeter.Counter  ──GetContextUsage RPC──► CLI meter
```

## 1. Capture usage in the streaming layer

`internal/llm/stream.go` — add two fields to `StreamEvent`:

```go
InputTokens  int // set on EventMessageStart (prompt tokens: system + tools + history)
OutputTokens int // set on EventMessageStop (final completion tokens)
```

`internal/llm/anthropic/stream.go` `convert()` — populate them on the two events
that already pass through:

- `message_start` (currently returns `EventMessageStart`): set
  `InputTokens` from the SDK's `message_start` usage (`message.usage.input_tokens`).
- `message_delta` (currently returns `EventMessageStop` with `StopReason`): set
  `OutputTokens` from the SDK's message-delta usage (`usage.output_tokens`).

No new event type. Exact SDK field paths are confirmed during implementation
against the installed `anthropic-sdk-go` version.

## 2. Surface usage through the loop

`internal/agent/toolloop.go`:

- `collectStream`: on `EventMessageStart` set `out.InputTokens = ev.InputTokens`;
  on `EventMessageStop` set `out.OutputTokens = ev.OutputTokens` (alongside the
  existing `StopReason`).
- `ToolLoopResult`: add `InputTokens int` and `OutputTokens int`.
- Set them from the **last** LLM call's `ChatResponse` before returning (every
  return path — final text, abort, max-iterations). The last call's input count is
  the full current context (history + all tool results this turn + system + tool
  schemas); its output is the final assistant message.

## 3. Snapshot the meter against the cloud window

`internal/agent/agent.go` — new exported method (the server reaches the meter
through the agent, mirroring `PersistentStore()`):

```go
// RecordContextUsage snapshots a conversation's context-window meter from
// provider-reported token usage, against the given model's window. No-op if no
// meter is configured or inputTokens <= 0 (avoids clobbering a prior reading
// when a provider reports no usage).
func (a *Agent) RecordContextUsage(convID, model string, inputTokens, outputTokens int) {
    if a == nil || a.meter == nil || convID == "" || inputTokens <= 0 {
        return
    }
    c := a.meter.Get(convID, model)
    c.SetMax(contextmeter.ModelMax(model)) // repair a counter lazily created with the local window
    c.Reset()
    c.AddCount(inputTokens)
    c.AddCount(outputTokens)
}
```

`internal/server/server.go` `streamProcessRequestWithToolLoop` — after
`RunToolLoop` returns (near the existing `persistToolLoopTurns` call), call:

```go
s.agent.RecordContextUsage(req.GetConversationId(), s.currentConfig.CloudModel,
    result.InputTokens, result.OutputTokens)
```

`used = input + output` — the context that rolls into the next turn.
`contextmeter.Counter` already exposes `SetMax`, `Reset`, `AddCount`, `Used`,
`Max`; `contextmeter.ModelMax(model)` already maps a model name to its window.

## 4. Safety / edge cases

| Case | Behavior |
|---|---|
| Provider reports no usage | `inputTokens <= 0` → no-op; prior reading kept |
| Local (Ollama) turn | Usage often absent → meter holds last value; v1 limitation |
| Loop abort / max iters | Record last available usage (set on every return path) |
| Counter created with local window earlier | `SetMax` repairs it to the cloud window |

The legacy `storeConversationTurn` snapshot stays unchanged — different code path,
same registry, keyed by conversation id.

## 5. CLI

No changes. The meter render and per-turn poll already exist; it begins showing
real values once the counter is populated.

## 6. Testing

- **Stream unit** (`anthropic/stream_test.go` or `toolloop` collectStream test): a
  fake event sequence with `message_start` input usage and `message_delta` output
  usage → resulting `ChatResponse` has both token fields set.
- **Loop unit** (`toolloop_test.go`): a scripted provider/stream that reports usage
  → `ToolLoopResult.InputTokens`/`OutputTokens` equal the **last** call's usage
  (verify multi-iteration takes the last, not the first).
- **Agent unit** (`agent` test): `RecordContextUsage(conv, cloudModel, in, out)`
  sets `Used() == in+out` and `Max() == ModelMax(cloudModel)`; a follow-up call
  with `inputTokens == 0` leaves the prior reading intact.
- **Server integration** (`toolloop_persist_test.go` sibling): after a tool-loop
  turn whose provider reports usage, `GetContextUsage(convID)` returns non-zero
  `tokens_used` and `model_max` equal to the cloud model's window.

## Out of scope

Local-provider usage reporting; prompt caching; the `/c` context view tab
(feature 3); compaction (feature 4).

## Key file references

| Concern | Location |
|---|---|
| StreamEvent | `internal/llm/stream.go` |
| Anthropic stream convert | `internal/llm/anthropic/stream.go:31` |
| ChatResponse usage | `internal/llm/provider.go:24` |
| collectStream / loop | `internal/agent/toolloop.go` |
| Legacy meter snapshot | `internal/agent/agent.go:149` |
| Meter accessor / wiring | `agent.go:46,270`; `cmd/cercano/main.go` |
| Counter / ModelMax | `internal/contextmeter/counter.go`, `tokenizer.go` |
| Tool-loop server path | `internal/server/server.go:845` |
