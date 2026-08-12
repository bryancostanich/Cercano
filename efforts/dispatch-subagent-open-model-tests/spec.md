# Dispatch / Sub-agent / Open-model test expansion

## Problem

Cercano's dispatch/sub-agent path is central to the product: frontier tokens should be reserved for frontier-grade reasoning, while mechanical recon and well-scoped work are delegated to local/open models. Recent debugging exposed that the system has some coverage, but not enough at the seams where failures actually appear:

- Sub-agents functionally used tools, but the low-signal diagnostic logged `called=[]` because dispatch telemetry re-derived tool usage from flattened history.
- We inferred an OpenAI-compatible streaming bug from second-order logs before capturing the real GLM-4.5-Air fragment layout.
- The open-model delegation surface has tests in individual packages, but too few cross-seam regressions that combine dispatch, flattened sub-agent history, tool-loop telemetry, provider streaming, logs/progress, and result classification.

The goal is to add a focused test matrix that would have caught these bugs earlier and makes future open-model delegation changes safer.

## Goals

1. Add deterministic, always-on Go tests for dispatch/sub-agent seams:
   - dispatch runner uses `ToolLoopResult.CalledTools`, not flattened `History`, for no-op/low-signal diagnostics.
   - flattened sub-agent history can omit `BlockToolUse` while telemetry and progress still know which tools ran.
   - agentic dispatch result text, lifecycle logs, progress events, `SubConversationID`, granted tools, and called tools remain consistent.
   - empty/no-tool/no-final-text cases are classified distinctly from successful tool-grounded dispatch.

2. Add OpenAI-compatible streaming fixture tests that exercise open-model-like tool-call shapes through the provider/collector/tool-loop boundary, not only the lowest-level stream reader.

3. Add one opt-in live smoke test for local/open-model delegation:
   - skipped unless explicitly enabled by environment.
   - uses the configured local/open provider path.
   - performs a tiny read-only dispatch.
   - asserts that the sub-agent invokes at least one tool and returns non-empty, grounded text.
   - never runs in normal `go test ./...` or continuous integration by default.

4. Improve self-development confidence without creating flaky default tests.

## Non-goals

- Do not change dispatch routing semantics unless a test exposes an actual bug.
- Do not require local model availability for default tests.
- Do not add broad end-to-end UI or CLI tests in this effort.
- Do not test every builtin capability through dispatch; use a small stub/read-only tool surface sufficient to exercise delegation contracts.
- Do not make the raw OpenAI stream tracer part of normal builds; it remains behind the `cercano_streamtrace` build tag.

## Existing coverage to preserve

Relevant current tests include:

- `internal/capabilities/builtins/dispatch_cap_test.go`
  - dispatch capability metadata, spec forwarding, default tier, tier knob, route header, empty task, nil service, dynamic permission tier.

- `internal/dispatch/agentic_test.go`
  - engine agentic routing and model override behavior.

- `internal/server/agentic_dispatch_test.go`
  - `runAgenticDispatch` R-tier defaults, explicit tool subsets, and engine runner wiring.

- `internal/server/agentic_observability_test.go`
  - persistence of subagent conversations, lifecycle logs, and progress events.

- `internal/agent/toolloop_test.go`
  - plain text termination, single tool calls, flattened tool-result history, `CalledTools`, concurrent R-tier calls, max-iteration degradation, permission paths.

- `internal/llm/openai/stream_test.go`
  - streaming text, tool delta events, late tool name, final-fragment tool name, reasoning recovery, usage chunks, and tool-call-only turns.

## Proposed test additions

### 1. Server / dispatch runner tests

Package: `source/server/internal/server`

Add or extend tests around `runAgenticDispatch`:

- `TestRunAgenticDispatch_LowSignalUsesCalledToolsNotFlattenedHistory`
  - Use a scripted provider that calls an R-tier tool then returns final text.
  - Run through `runAgenticDispatch`, which enables `FlattenToolResults` for sub-agents.
  - Capture logs.
  - Assert the low-signal/done diagnostic contains `called=[r_read]` (or the granted tool name) rather than `called=[]`.
  - Assert the result text remains the final answer and no suspicious warning is prepended.

- `TestRunAgenticDispatch_ToolCallNoFinalTextClassifiedSeparately`
  - Provider calls a tool and then ends with no final text or hits the final no-tools degradation path, depending on the existing loop behavior.
  - Assert the result is not treated the same as "model made no tool calls".
  - Assert diagnostics include the called tool and a clear reason.

- `TestRunAgenticDispatch_NoToolCallsLowSignalRemainsNoTool`
  - Provider returns immediate text with no tools, under a read-only grant.
  - Assert diagnostics distinguish this from a tool-grounded run.

- `TestRunAgenticDispatch_ProgressAndLogsAgreeOnToolNames`
  - Capture progress events and logs for a tool-using sub-agent.
  - Assert planned/running/complete progress names match `called=[...]` in logs.

### 2. Agent tool-loop tests

Package: `source/server/internal/agent`

Extend coverage around flattened history and tool telemetry:

- `TestToolLoop_CalledToolsRecordsMultipleDistinctToolsInOrder`
  - Provider returns two different tool calls in one turn or across turns.
  - Assert `CalledTools` contains first-seen unique names in order.

- `TestToolLoop_CalledToolsDeduplicatesRepeatedTool`
  - Provider calls the same tool multiple times.
  - Assert `CalledTools` contains one entry.

- `TestToolLoop_CalledToolsSurvivesMaxIterationDegrade`
  - Use the looping provider that reaches `MaxToolLoopIterations` and final no-tools answer.
  - Assert `CalledTools` still records the tool that kept being invoked.

- `TestToolLoop_FlattenPersistsFullToolUseButReturnsFlattenedHistory`
  - If the existing harness can observe `persistTurn`, assert the persisted turn keeps `BlockToolUse` while returned `History` is flattened.
  - If persistence hooks are not exposed cleanly at this layer, keep this assertion at the server persistence layer instead.

### 3. OpenAI-compatible streaming / provider fixture tests

Package: `source/server/internal/llm/openai` and possibly `source/server/internal/agent`

Add fixture coverage for open-model streaming shapes:

- `TestStreamChat_ToolArgumentsArriveBeforeNameAndID`
  - A malformed but plausible compatibility-server shape: arg fragments arrive before name/id.
  - Assert collector still produces a coherent block when name eventually arrives, or produces a loud/diagnosable failure if the stream is truly malformed.

- `TestStreamChat_MultipleToolCallsInterleavedByIndex`
  - If supported by the streaming contract, simulate two tool-call indexes with fragments alternating.
  - Assert each call's name/id/args are assembled separately.
  - If the current implementation only supports one open index at a time, document and test the supported behavior rather than silently pretending interleaving is handled.

- `TestOpenAIStreamCollectedToolUseFeedsRunToolLoop`
  - Use an HTTP/SSE fixture provider, not the simple `mockProvider`, and run it through `RunToolLoop` with a stub registry.
  - Assert the tool executes and `CalledTools` is populated.
  - This closes the gap between low-level stream-reader correctness and the agent tool loop.

### 4. Dispatch capability tests

Package: `source/server/internal/capabilities/builtins`

Add or extend:

- `TestDispatch_Execute_EmptySubagentTextStillIncludesDiagnostics`
  - Stub dispatch service returns empty text but useful metadata (`Model`, `Provider`, `GrantedTools`, maybe `Suspicious`).
  - Assert the capability response includes route/tools/warning headers so the parent model does not receive a silent blank.

- `TestDispatch_Execute_SuspiciousWarningIsPrepended`
  - Stub dispatch returns `Suspicious=true` and reason.
  - Assert warning header appears before the body.

### 5. Opt-in live open-model smoke test

Location options:

- `source/server/internal/server/live_open_model_dispatch_test.go`, or
- `source/server/internal/dispatch/live_open_model_test.go`.

Behavior:

- Skipped unless `CERCANO_LIVE_OPEN_MODEL_TEST=1`.
- Should also require a small explicit workdir/file fixture, generated in `t.TempDir()` if possible.
- Uses the same configured provider selection code as dispatch, or a narrowly constructed local/open provider if that is more stable.
- Runs a read-only task like: "Use Read to open <temp file> and report the token on the first line." 
- Asserts:
  - no error,
  - non-empty result text,
  - text contains the sentinel token,
  - result metadata identifies an open/local route where available,
  - telemetry/logs show at least one called tool.

The live smoke test must never require network/cloud credentials and must be skipped by default with a clear message explaining how to enable it.

## Acceptance criteria

- New/extended hermetic tests fail against the pre-`CalledTools` dispatch telemetry behavior and pass with the current implementation.
- Normal `go test ./...` does not require local model availability.
- `go test ./internal/agent/... ./internal/server/... ./internal/capabilities/... ./internal/llm/...` passes.
- The opt-in live smoke test is skipped by default and documented.
- The live smoke test can be run manually on a configured development machine and validates a real local/open-model read-only dispatch.
- Tests are targeted enough to diagnose future failures: a break in routing, streaming assembly, flattened-history telemetry, or dispatch capability rendering should point to a specific package/seam.

## Open design decisions

Decision 1 is approved: use always-on hermetic tests plus one skipped-by-default live open-model smoke test.

Remaining design choices to settle during implementation, if ambiguity arises:

1. Whether multi-tool interleaving is supported by the current OpenAI stream reader. If not, write a loud test documenting the current supported behavior rather than widening the parser silently.
2. Whether the live smoke test should use the full server dispatch engine or a narrower provider fixture. Prefer full dispatch path if stable enough; fall back to a narrower fixture if configuration makes it too brittle.
3. Whether log assertions should require exact message formatting or only semantically important substrings. Prefer substrings to avoid fragile tests while still pinning the important fields (`called=[...]`, model, provider, conversation id).
