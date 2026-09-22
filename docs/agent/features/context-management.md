# Advanced Context Management

## What it is

Rolling background compaction digests older conversation turns into layered
summaries while retaining recent working context. The agent sends a compacted
view rather than replaying an ever-growing transcript on every request.
Compaction runs asynchronously, so requests do not wait for a summarizer.
The stored conversation history remains separate from that compacted view.

## Why it matters

Keep working through long investigations and large refactors without stopping
for periodic summarization. A smaller active context leaves more space for
current work and reduces repeated history tokens sent to the model. That can
reduce token costs, although compaction itself consumes inference and savings
depend on your models and workload.

## Try it

- `/context` — inspect active context usage.
- `/compact` — start incremental background compaction.
- `/context-regen` — rebuild compacted context from stored turns.
- `/clear-compacted-context` — remove summaries and rehydrate from stored turns.
- `/elide-context` — reclaim context by stubbing prior tool outputs without inference.

Use the live context meter to follow a long session's working context.

## Controls and limitations

- Background work can fall behind. At the hard limit, request assembly schedules
  compaction and reduces the outgoing view with tool-output elision and, if
  necessary, removal of oldest messages—not a blocking summarizer call.
- Summaries are lossy. A manageable context is not a guarantee that every old
  detail is present in every request. Stored history and model-visible context
  are different things.
- Compaction thresholds and retention behavior are configurable. Clearing a
  summary does not make an arbitrarily large raw history fit the model window.
- `/elide-context` affects the in-memory send view, not stored raw turns; its
  effect resets on agent restart.

See the [context-management design](../context-management/design.md),
[non-blocking compaction design](../context-management/compaction/compaction-nonblocking-design.md),
and [agent command reference](../README.md).

Back to the [feature index](README.md).
