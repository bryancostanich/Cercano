# llama-server Memory Guard and Durable Runtime Events

Stop two 68 GB llama-servers from being simultaneously wired on a 128 GB machine, and
make the runtime's lifecycle/memory behavior durable enough to diagnose next time.

Anchor: `efforts/llama-server-memory-guard/spec.md`. Decisions 1–4 there are load-bearing.

Phase order is deliberate: instrumentation first (Phase 1) so that the guard's
threshold in Phase 4 is calibrated against recorded reality rather than a guess, and
so any regression introduced later is visible in the same log.

## Phase 1 — Durable runtime events in `crashlog`

Objective: route llama-server lifecycle events into `~/.config/cercano/crash.log` as
structured NDJSON, on the same timeline as signal/panic records (Decision 1).
Files: `source/server/internal/crashlog/crashlog.go`,
`source/server/internal/crashlog/crashlog_test.go`,
`source/server/internal/localruntime/llamaserver/provider.go` (`emit`, line 812).
Tests: a runtime event round-trips through the writer with its `Extra` fields intact;
events append rather than truncate; a nil/unconfigured writer is a no-op and never
panics; existing crash-record tests still pass.

Note (execution): the plan originally said to write events from inside
`Provider.emit`. That proved wrong — `emit` is also the sink for `pipeLogs`, so every
llama-server stdout line flows through it, and its signature carries only strings
(no PID/port). Durable events are therefore written by an explicit `Provider.event`
helper called at each lifecycle site, where the structured data actually exists.
`emit` is left untouched so the UI ring buffer is provably unchanged. Captured as
autonomous decision #1.

- [x] Add `KindRuntimeEvent` and a `LogRuntimeEvent` method to `crashlog`
- [x] Amend the `crashlog` package doc to describe a durable operational event log
- [x] Give `Provider` an optional crashlog writer handle defaulting to nil/no-op
- [x] Add a `Provider.event` helper and call it at each lifecycle site
- [x] Wire the real writer into the provider construction path used by `cmd/agent`
- [x] Run `go test ./internal/crashlog/... ./internal/localruntime/...`

## Phase 2 — Wired-memory probe in `sysram`

Objective: report non-evictable (wired + compressed) memory, the quantity that
actually predicts the freeze (Decision 3).
Files: `source/server/internal/sysram/sysram_darwin.go`, `sysram_linux.go`,
`sysram_other.go`, and a new `sysram_test.go`.
Tests: darwin probe returns a plausible non-zero value below `Total()`; unsupported
platforms return the unknown sentinel; callers can distinguish unknown from zero.

- [ ] Verify vm_stat page accounting against live output and record the numbers
- [ ] Add `NonEvictable()` to `sysram` following the existing per-OS build-tag structure
- [ ] Implement the darwin probe from host_statistics64 page counts
- [ ] Implement or explicitly decline the linux probe, returning unknown if declined
- [ ] Keep `sysram_other.go` returning the unknown sentinel
- [ ] Run `go test ./internal/sysram/...`

## Phase 3 — Synchronous teardown

Objective: stop reporting success on signal delivery; wait for the process to
actually die and release its wired memory (Decision 2, first half).
Files: `source/server/internal/localruntime/llamaserver/process_unix.go`
(`killProcess` line 18, `terminateGroup` line 51), `process_other.go`,
`provider.go` (`Stop` line 366, `kill` line 771), and a new/extended test file.
Tests: a stopped instance's PID is gone by the time `Stop` returns; a process
ignoring SIGTERM is escalated to SIGKILL and still confirmed dead; the wait is
bounded and returns an error rather than hanging forever; concurrent `Start` calls
are not blocked while a teardown waits.

- [ ] Make `killProcess` wait for confirmed death via bounded `processAlive` polling
- [ ] Confirm death after SIGKILL escalation in `terminateGroup` too
- [ ] Audit `Provider.Stop` and `Provider.kill` so no wait happens while holding `p.mu`
- [ ] Emit a `stop` runtime event recording wait duration and SIGKILL escalation
- [ ] Keep `process_other.go` in sync so non-Unix builds compile
- [ ] Run `go test ./internal/localruntime/...`

## Phase 4 — Pre-spawn reap barrier and memory guard

Objective: the surviving process defends itself at the moment of danger — confirm no
doomed-but-resident server holds memory, then refuse the spawn if the projection
exceeds physical RAM (Decision 2 second half, Decisions 3 and 4).
Files: `source/server/internal/localruntime/llamaserver/provider.go` (`Start` line
222, `startProcess` line 490), `orphans.go` (`SweepOrphans` line 140, `ownerAlive`
line 190), `provider_reuse_test.go` or a new `provider_guard_test.go`.
Tests: guard refuses when projection exceeds the limit and **no process is spawned**;
guard permits when it fits; unknown probe falls back to registry arithmetic rather
than refusing outright; the barrier reaps a doomed server and then permits the spawn;
error text contains the real figures; existing reuse/adopt tests still pass.

- [ ] Add the reap barrier to `Start` before `choosePort` and `startProcess`
- [ ] Widen the reap predicate so a draining owner's server is also a candidate
- [ ] Confirm reaped processes are dead by polling, without holding `p.mu`
- [ ] Implement the memory projection from wired plus weights plus KV cache
- [ ] Fall back to registry arithmetic when the probe returns unknown
- [ ] Refuse over-budget spawns via the existing `InstanceFailed` and `LastError` path
- [ ] Include projected, current, model size, KV, headroom and limit in the error text
- [ ] Emit a `spawn` runtime event recording projected against actual
- [ ] Run `go test ./internal/localruntime/...`

## Phase 5 — End-to-end verification

Objective: prove the guard actually prevents the overlap, rather than trusting unit
tests.
Files: no production edits; a throwaway probe script at most.

- [ ] Start GLM-4.5-Air through the runtime dashboard and record actual resident size
- [ ] Compare recorded projection against measured actual and note the delta
- [ ] Restart the agent while GLM is live and confirm the barrier reaped and waited
- [ ] Confirm a second concurrent large-model start is refused with the numeric error
- [ ] Confirm `crash.log` now contains spawn, reuse, reap and stop records

## Phase 6 — Cleanup and checkpoint

- [ ] Remove any throwaway probe scripts
- [ ] Confirm `go build ./...` and `go test ./...` pass from `source/server`
- [ ] Checkpoint the guard, teardown and event log with a conventional-commit message
- [ ] Record the deferred override and registry-flock follow-ups in the effort dir
