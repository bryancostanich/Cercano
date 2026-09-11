# Search responsiveness follow-up

The background-worker change was incomplete: `refreshVisibleDynamicViewport` still switched to a full transcript rebuild whenever search was visible, and `updateConversationSearch` invalidated matching even when searchable messages had not changed. Both could run repeatedly while the application streamed output.

## Changes

- Keep the dynamic refresh path with search open. Refresh off-screen dynamic user/assistant messages as well, but not off-screen tool output. A genuine shape change still triggers layout reconciliation.
- Compare searchable message identity, source text, row position, and width before invalidating results. Unchanged animation/refresh events no longer schedule matching.
- Preserve the existing background command, immutable snapshots, one-worker coalescing, stale-result rejection, and immediate editor/Escape handling. Queries remain incremental; this change does not introduce a typing debounce.

## Measurements

Synthetic fixture: 1,000 assistant messages, eight identical short paragraphs per message, 8,000 matches for each query. Measured on an Apple M5 Max with a 200 ms benchmark duration. These are component measurements, not live terminal latency guarantees.

| Component | Before | After |
|---|---:|---:|
| Refresh with search open | 17.61 ms | 15.91 µs |
| Refresh allocations | 648,015 / 4.89 MB | 10 / 176 B |

Search-closed refresh measured 1.3–2.2 µs. The remaining search-open overhead is the metadata comparison and field sizing.

Additional component measurements after the fix:

| Component | Time | Allocated bytes |
|---|---:|---:|
| UI snapshot creation | 0.103 ms | 0.76 MB |
| Cold background indexing/matching | 79.09 ms | 103.8 MB |
| Warm background matching | 3.11 ms | 4.50 MB |
| UI application of 8,000 matches | 0.719 ms | 3.40 MB |
| View rendering | 0.149 ms | 0.09 MB |

Cold projection remains allocation-heavy. It runs in the worker, and subsequent queries reuse the projection. No claim is made that all future large-history performance issues are resolved.

## Reproduction and verification

From `source/clients/cli`:

```sh
go test ./internal/ui -run '^$' -bench BenchmarkConversationSearch -benchmem -benchtime 200ms
go test -race ./internal/ui -run 'TestConversationSearch|Test.*Dynamic' -count=1
go test ./internal/ui ./internal/slash -count=1
go build ./...
```

The focused idle-refresh regression failed before the change and passes afterward. Off-screen streaming-search coverage, focused race tests, full UI/slash tests, and CLI build pass. No interactive terminal verification or installed-binary replacement was performed for this follow-up.

## Opt-in live search telemetry

Launch a CLI containing this change with `CERCANO_SEARCH_TRACE=1`. Open search,
type `t`, wait for the stall, finish `test`, then close search. The local JSONL
log is `~/.config/cercano/search-perf.jsonl`; `CERCANO_SEARCH_TRACE_LOG` overrides
its path. Restart without the variable after capture. No query text, key values,
message bodies, conversation IDs, or remote telemetry are recorded.

Records include PID, build revision/dirty flag, session, search revision, query
byte length, history size, match counts, timestamps, and elapsed milliseconds.
Correlate by PID/session/revision; each begin/end pair also has a span ID.

- `update.*.begin/end`: reducer timing, including snapshot scheduling. Only the
  message type is logged. Context is captured at entry; `invalidate` records
  the new revision and whether input or layout caused it.
- `snapshot.begin/end`, `worker.queue`: UI snapshot cost and worker scheduling delay.
- `worker.begin/end`, `index.begin/end`: background processing spans.
- `projection.total`, `matching.total`: component timings, cache reuse, and match
  count. Entries taking 20 ms emit `projection.slow_entry` or
  `matching.slow_entry`, with source byte count and entry index in `count`.
- `worker.results`: match count and cached-entry count (`count`).
- `result.queue`, `apply.begin/end`, `result.applied`: UI queue delay, application
  duration, and accepted match count. `result.discard` explains stale results.
- `view.begin/end`: model rendering, not physical terminal drawing or input
  latency before `Model.Update` receives a key.

Only a background writer accesses the filesystem. A bounded 1,024-event queue
never waits for disk; `dropped_total` counts discarded records. Files are created
with mode 0600. Writer failures do not interrupt the UI; ensure the log exists
and its parent is writable. No rotation is implemented: capture briefly. Abrupt
exit may lose queued events; wait briefly after closing search before exiting.

Tests cover stage/span correlation, no private text, restricted file permissions,
disabled tracing, writer errors, and nonblocking queue overflow. Focused search
race tests, full UI/slash tests, and the CLI build pass. This instruments the live
problem; it does not claim to diagnose or fix the newly reported stall.
