# Dispatch reasoning capture

Dispatch history records what a model did — requests, responses, tool calls,
compaction passes — but redacts *why*. Reasoning blocks are reduced to a byte
count:

```
[reasoning omitted: 639 bytes]
```

That is correct for normal operation. Reasoning restates conversation and tool
content, so storing it by default would put a second copy of everything on disk.

It also makes some failures undiagnosable. When a dispatch behaves oddly — the
same file read repeatedly, an investigation that never becomes an edit — the
tool calls show the behavior but not the intent. Reconstructing intent from tool
calls alone is guesswork, and in one investigation it produced four confident
wrong diagnoses in a row before the reasoning text settled the question in a
single run.

## Enabling it

```yaml
capture_dispatch_reasoning: true
```

Off by default. Read at wiring time, so a change takes effect on the next start
rather than mid-session. Applies to both the in-process and worker dispatch
paths.

With it on, `model_response` events in `dispatch_events` carry the model's
plaintext reasoning alongside the blocks it produced.

## What is and is not captured

**Captured:** plaintext reasoning that arrives on the turn, such as the
`reasoning_content` field GLM-family models return through the
chat-completions API.

**Not captured:** opaque Responses-API reasoning, identified by a
`ReasoningID`. That is encrypted round-trip state rather than readable thought,
so storing it costs space and yields nothing to read.

**Not captured:** reasoning inside history snapshots. Capture applies only to
the `model_response` event that produced it. Snapshots keep the redaction
marker, so one turn's reasoning is stored once rather than re-stored in every
later snapshot containing that turn.

## Bounds and safety

- Each reasoning block is capped at 32 KiB, truncated on a rune boundary, with
  the truncation disclosed in the stored text. A single block in the
  aperture-reuse investigation ran to 15,268 characters; unbounded capture would
  grow the conversation database quickly.
- Capture never mutates the caller's blocks. The same blocks are returned to the
  provider on the continuation, so corrupting them would change model behavior
  rather than observe it. A test pins this.
- Nothing captured is replayed to a provider. This is a write-only diagnostic
  path.
- Image redaction is unaffected.

## What it is for

Reading intent at a specific moment. For example, a dispatch that re-read one
file seven times looked like a memory or compaction failure from the outside.
The reasoning showed otherwise:

```
The file is 405KB, ~10000 lines. I need to be careful reading it.
```

followed by ascending, non-overlapping page requests. The model was paging
through a truncated file deliberately, and the truncation note it was given
did not say where to continue. That was a tool defect, not a memory problem, and
no amount of tool-call analysis had revealed it.

Turn it on while diagnosing, and off again afterward.
