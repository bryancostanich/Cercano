# Plan: Dispatch / sub-agent / open-model test expansion

## Scope

Add focused tests that protect the dispatch/sub-agent/open-model seams that recently failed or were hard to diagnose. Default tests must be hermetic and fast. Add one skipped-by-default live smoke test for local/open-model dispatch.

## Phase 1 — Pin dispatch telemetry against flattened sub-agent history

Files likely touched:

- `source/server/internal/server/agentic_observability_test.go`
- possibly `source/server/internal/server/agentic_dispatch_test.go`

Tasks:

1. Add `TestRunAgenticDispatch_LowSignalUsesCalledToolsNotFlattenedHistory`.
   - Reuse `observabilityDispatchRig` or a similar server/provider fixture.
   - Capture logs while `runAgenticDispatch` calls one R-tier tool and returns final text.
   - Assert log output contains `called=[r_read]` or equivalent populated called list.
   - Assert it does not contain `called=[]` for that run.
   - Assert no suspicious warning is prepended to the result.

2. Add `TestRunAgenticDispatch_ProgressAndLogsAgreeOnToolNames` or extend the existing progress test.
   - Capture progress events and logs.
   - Assert planned/running/complete progress names match the called list in logs.
   - Use substring checks, not full-line golden assertions.

3. Add a no-tool baseline test if not already covered clearly.
   - Provider returns text immediately.
   - Assert any low-signal/no-op classification is about no tools, not a hidden tool telemetry failure.

Verification:

```bash
cd source/server
go test ./internal/server/ -run 'RunAgenticDispatch_(LowSignal|Progress|NoTool|Logs)' -count=1 -v
```

## Phase 2 — Strengthen agent tool-loop telemetry tests

Files likely touched:

- `source/server/internal/agent/toolloop_test.go`

Tasks:

1. Add `TestToolLoop_CalledToolsRecordsMultipleDistinctToolsInOrder`.
   - Register two stub read-only tools.
   - Provider calls both.
   - Assert `CalledTools == []string{"tool_a", "tool_b"}` in first-seen order.

2. Add `TestToolLoop_CalledToolsDeduplicatesRepeatedTool`.
   - Provider calls the same tool multiple times.
   - Assert only one entry appears.

3. Add `TestToolLoop_CalledToolsSurvivesMaxIterationDegrade`.
   - Reuse `loopingProvider`.
   - Assert final no-tools answer still has `CalledTools` populated.

4. If practical, add/extend a persistence-facing assertion at the server layer rather than the raw tool-loop layer:
   - persisted subagent conversation includes full `tool_use`, while returned model-facing history can be flattened.

Verification:

```bash
cd source/server
go test ./internal/agent/ -run 'CalledTools|Flatten|HitsCap' -count=1 -v
```

## Phase 3 — Add OpenAI-compatible streaming fixture coverage through collection and tool loop

Files likely touched:

- `source/server/internal/llm/openai/stream_test.go`
- possibly a new helper/test file in `source/server/internal/agent/` if running the HTTP/SSE fixture through `RunToolLoop` is cleaner there.

Tasks:

1. Add `TestStreamChat_ToolArgumentsArriveBeforeNameAndID` or a more precise variant if the library type cannot represent args-before-name cleanly.
   - Assert either robust recovery or a loud, documented behavior.
   - Do not silently accept empty tool names.

2. Evaluate support for interleaved multi-index tool-call streams.
   - If current implementation supports only one open tool index at a time, write a test documenting that behavior or defer widening the parser.
   - If straightforward, add `TestStreamChat_MultipleToolCallsInterleavedByIndex`.

3. Add an integration-style hermetic test that feeds an OpenAI-compatible SSE fixture into `RunToolLoop`.
   - The provider should be a real `openai.Client` pointed at an `httptest.Server`, not the simplified `mockProvider`.
   - The SSE stream should call a stub tool, then return final text on the next provider call.
   - Assert the stub tool executed and `ToolLoopResult.CalledTools` is populated.

Verification:

```bash
cd source/server
go test ./internal/llm/openai/ -run 'Tool|Stream' -count=1 -v
go test ./internal/agent/ -run 'OpenAI|SSE|CalledTools' -count=1 -v
```

## Phase 4 — Add dispatch capability rendering regressions

Files likely touched:

- `source/server/internal/capabilities/builtins/dispatch_cap_test.go`

Tasks:

1. Add `TestDispatch_Execute_EmptySubagentTextStillIncludesDiagnostics`.
   - Stub dispatch returns empty `Text` but route/tool metadata.
   - Assert returned capability text still includes route/tools header so parent model does not receive a silent blank.

2. Add `TestDispatch_Execute_SuspiciousWarningIsPrepended`.
   - Stub dispatch returns `Suspicious=true` and reason.
   - Assert warning header appears before body text.

Verification:

```bash
cd source/server
go test ./internal/capabilities/builtins/ -run 'Dispatch_Execute_(Empty|Suspicious|IncludesRoute)' -count=1 -v
```

## Phase 5 — Add skipped-by-default live local/open-model smoke test

Files likely touched:

- new `source/server/internal/server/live_open_model_dispatch_test.go`, or another package if a cleaner configured dispatch harness already exists.
- `docs/agent/self-dev.md` for how to run it.

Tasks:

1. Create a live smoke test guarded by `CERCANO_LIVE_OPEN_MODEL_TEST=1`.
   - If unset, call `t.Skip` with a clear message.
   - Generate a temp file with a sentinel token.
   - Run a tiny read-only dispatch task asking the sub-agent to `Read` that file and report the token.
   - Prefer using the real dispatch/provider selection path if available without excessive setup; otherwise use the narrowest stable local/open provider path and document what it covers.

2. Assertions:
   - no error,
   - non-empty result text,
   - result text contains the sentinel,
   - open/local route metadata is present where exposed,
   - logs or result metadata show at least one called tool.

3. Document usage in `docs/agent/self-dev.md`, including prerequisites and the fact that it is skipped by default.

Verification:

Default:

```bash
cd source/server
go test ./internal/server/ -run LiveOpenModel -count=1 -v
printf 'expected: SKIP unless CERCANO_LIVE_OPEN_MODEL_TEST=1\n'
```

Manual live:

```bash
cd source/server
CERCANO_LIVE_OPEN_MODEL_TEST=1 go test ./internal/server/ -run LiveOpenModel -count=1 -v
```

## Phase 6 — Final verification

Run targeted and broader suites:

```bash
cd source/server
go test ./internal/agent/... ./internal/server/... ./internal/capabilities/... ./internal/llm/... -count=1
go test ./... -count=1
```

If the full suite is too slow or requires external services unexpectedly, record the failing/skipped package and run the relevant stable subset; do not hide failures.

## Checkpoint

After implementation and verification, checkpoint with a conventional commit:

```text
test(dispatch): expand sub-agent and open-model delegation coverage
```

Commit body should summarize:

- dispatch telemetry regression tests,
- tool-loop `CalledTools` coverage,
- OpenAI-compatible streaming fixture coverage,
- dispatch capability rendering tests,
- opt-in live smoke test and docs,
- verification commands run.
