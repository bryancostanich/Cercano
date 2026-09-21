# Warm-worker log forwarding and Lunie dispatch investigation

## Confirmed log-forwarding defect

`internal/worker/spawn.go` used `io.ReadAll` on the child stdout/stderr pipe before logging its contents. Warm workers keep that pipe open after a dispatch ends, so completion or budget exhaustion does not make their diagnostic output visible. The worker lifecycle is intentional; buffering all output until process exit is not.

A regression using a pipe held open after a newline failed with the original implementation: `worker output withheld while worker pipe remains open`.

The fix forwards complete lines immediately, drains oversized lines in bounded 32 KiB fragments, flushes final unterminated output on EOF, closes the reader, and reports read errors. Each record carries the worker PID. It does not change worker reuse or terminate workers to obtain logs. Small unterminated fragments remain buffered until newline or EOF.

## Dispatch evidence

Read-only inspection of persisted conversation `a7013ccde6ecbeacdeddf432` found:

- Model recorded as `zai-org/GLM-5.3-Flash`.
- September 20 local time: started 19:24:11, last persisted turn 20:32:21.
- Task explicitly requested implementation of an approved live globe integration slice and granted Edit/Write and command execution.
- 94 requested calls: 72 Read, 15 Grep, five Bash, two Glob. The final requested Read has no tool result; do not count it as completed execution.
- No Edit/Write calls or test execution; the command calls performed initial discovery and inspection.
- The full plan was read five times, mission_camera.rs seven times, mission_publication.rs seven times, and mission_scene.rs six times. Hashes of the results for each identical full-file request were identical across repeats.
- Tool results contained approximately 1.26 million characters cumulatively. No persisted tool-result error flags were present. Some broad reads/searches reached output caps and explicitly requested narrower queries.

This establishes repeated successful inspection without implementation progress. It does not establish why the model repeated itself. In particular, the stored tool transcript does not show the compacted request views or summary fidelity; compaction-induced loss of working state remains an unproven hypothesis. A larger cumulative budget alone is not a demonstrated remedy.

The installed executable reported revision ef0ce868468c (worker compaction fix), with a dirty build flag, and was built before the dispatch's worker started. The still-live worker's buffered telemetry prevented checking exact compaction events without disrupting it. This patch has not been deployed and does not recover already-buffered output from the old host.

Existing consecutive-tool-error checks do not stop successful repeated reads. Finished-dispatch no-op validation is not an in-flight progress check.

## Verification

- Observed regression failure before replacing ReadAll.
- `go test ./internal/worker ./internal/server ./cmd/cercano -count=1` passed.
- `go test -race ./internal/worker -run TestWorkerOutput -count=1` passed.
- Tests cover forwarding before pipe closure across successive dispatch-like messages, oversized lines, final partial output, and read-error cleanup.
- No agent restart, live model experiment, or process termination was performed.
