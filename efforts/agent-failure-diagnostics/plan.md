# Agent Failure Diagnostics Log Plan

## Chosen direction

Create a new failure-only structured JSONL log at `~/.config/cercano/failures.jsonl`, separate from the existing `turn-routing.jsonl` routing log.

The failure log should collect durable, sanitized diagnostics for failures encountered in the main chat/tool loop and in dispatch/delegation. It should not replace user-visible chat errors; it should make later debugging easier.

## Phase 1: Add the writer package

1. Add a small internal package, likely `source/server/internal/failurelog`, modeled after `routinglog.Writer` but semantically dedicated to failures.
2. Implement:
   - `DefaultPath() string` returning `~/.config/cercano/failures.jsonl`;
   - `NewWriter(path string) (*Writer, error)`;
   - `Log(event string, fields Event)`;
   - `Close() error`.
3. Preserve the current privacy posture:
   - generic map fields;
   - timestamp and event injection;
   - no special support for prompt/tool payloads.
4. Add unit tests for:
   - default-path shape if practical;
   - append behavior;
   - valid JSONL output;
   - nil writer/no-op behavior;
   - marshal failure does not panic.

## Phase 2: Wire the failure log into server construction

1. Add a failure-log field to server/worker dependencies where the routing log is already wired.
2. Construct the writer during server/worker setup near the existing routing log construction.
3. Make construction failure non-fatal in the same spirit as existing diagnostic logging: if the log cannot be opened, continue running and do not break chat.
4. Ensure shutdown closes the writer if the surrounding lifecycle has an established close path.

## Phase 3: Instrument main chat failure paths

1. In the main runner/tool-loop orchestration, log representative final failure/degraded states:
   - provider/model selection errors;
   - provider errors returned from the loop;
   - fallback/retry exhaustion;
   - tool-loop guard aborts such as max iterations or consecutive all-error attempts;
   - permission/tool errors when they become final or meaningfully degrade the turn.
2. Include available safe metadata:
   - conversation id / turn id / request id if present;
   - provider/profile/model if present;
   - tool name for tool failures;
   - failure class;
   - sanitized short message;
   - duration or attempt counts where already available.
3. Avoid logging prompt text, tool arguments, tool results, and response bodies.
4. Do not change chat-visible error behavior.

## Phase 4: Instrument dispatch/delegation failure paths

1. Pass or otherwise expose the failure logger to the dispatch/workflow tool implementation and delegated engine.
2. Generate or reuse a stable `dispatch_id` per dispatch if one already exists; otherwise add one locally for logging and returned diagnostics.
3. Log dispatch failures such as:
   - invalid dispatch arguments;
   - model/locus resolution failure;
   - delegated run failure;
   - delegated tool-loop failure;
   - suspicious/degraded completion if detectable, such as no tool calls for a read-only recon task.
4. Include safe correlation metadata where available:
   - parent conversation id / turn id / request id;
   - dispatch id;
   - requested tier/locus/model if available;
   - granted tool names;
   - failure class and sanitized message.
5. Keep the existing parent chat tool result behavior unchanged except where adding a dispatch id is already consistent with current output.

## Phase 5: Verification

Run targeted server tests from `source/server`:

1. `go test ./internal/failurelog`
2. Targeted tests for touched packages, likely:
   - `go test ./internal/runner`
   - `go test ./internal/hostsvc/tools`
   - `go test ./internal/dispatch`
   - any worker/server package tests affected by wiring
3. If wiring changes are broad, run:
   - `go test ./internal/...`

## Risks and mitigations

- **Risk: logging sensitive payloads.** Mitigation: only log explicit metadata fields and sanitized error strings; never include prompts, tool args, tool outputs, or provider bodies by default.
- **Risk: logging failures becomes a new source of failures.** Mitigation: writer methods remain best-effort and never return errors to caller after construction.
- **Risk: too many noisy entries.** Mitigation: first slice logs final failures and meaningful degraded states, not every transient retry.
- **Risk: missing correlation ids.** Mitigation: log whatever ids are already available first; do not block the feature on a full request-id redesign.
- **Risk: duplicate concepts with `routinglog`.** Mitigation: keep routing log unchanged; use failure log only for failure/degraded diagnostic events.

## Open implementation questions for execution

- Which existing types already carry conversation/turn/request ids at the exact main-loop call sites?
- Does dispatch already generate a stable id, or should the first slice add a local `dispatch_id`?
- Which failure conditions are represented as typed errors versus message strings?
- Are server and worker lifecycle close paths sufficient to close the new writer, or should it be best-effort and process-lifetime scoped?
