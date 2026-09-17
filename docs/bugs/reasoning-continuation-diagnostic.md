# Reasoning continuation diagnostic — 2026-09-16

## Status: offline mechanism verified; live cause unproven

The test-only harness is in
`source/server/internal/llm/openai/reasoning_continuation_diagnostic_test.go`.
It does not change production behavior, access credentials, execute tools, or
contact a cloud model. It is not a fix for repeated audit tool calls.

Run:

```sh
cd source/server
go test ./internal/llm/openai -run '^TestReasoningContinuationDiagnostic' -count=1
go test ./internal/llm/openai -count=1
```

## Hypothesis and expected result

The current adapter collects tool calls but does not replay the separate
`reasoning_content` field on the next request. Missing continuation state may
contribute to repeated investigation, but this remains a hypothesis about the
live model, not a demonstrated causal explanation.

Before running the fixture, the expected result is:

1. Both arms receive exactly the same initial response, including two opaque
   reasoning fragments and a tool call.
2. Both use the actual OpenAI-compatible adapter and stream collector, and return
   an identical recorded result with the matching tool-call ID.
3. The drop arm leaves production request encoding unchanged. The preserve arm
   adds the exact concatenated reasoning to the corresponding assistant message
   in a test-only HTTP transport wrapper.
4. All paired request fields must compare equal after removing that one field.

The synthetic endpoint deliberately repeats its call when continuation is absent
and completes when it is present. **That scripted behavior validates the harness,
not GLM behavior.** Fresh call IDs do not hide identical action/result batches.
This detector is deliberately narrower than arbitrary multi-request cycle
recognition and does not establish task completion beyond a terminal stop.

## Isolation and bounds

- Fixture lookup only: `Read` with the exact recorded arguments. No capability
  execution or filesystem access; unknown calls fail closed.
- Four requests maximum per normal arm, each requesting at most 128 output
  tokens (512 requested output tokens per arm), ten-second context deadline,
  and 2 MiB request/response capture limits. These are fixture limits, not a
  recommendation for the eventual live audit's reasoning budget.
- Budget exhaustion, repeated unchanged call batches, and nonterminal finishes
  are distinct from completion.
- Missing captured reasoning in the preserve arm, incomplete streams, oversized
  captures, unknown calls, and cancelled contexts are rejected.
- Bodies and opaque reasoning remain in memory, with no credential lookup or
  raw content logging. State belongs to one arm; the global HTTP transport is
  not modified. Existing adapter structural diagnostics still run.
- The capture buffers complete SSE responses and supports a single completion
  choice. It is an offline probe, not a production streaming implementation or
  an already-integrated agent diagnostic facility.

## Verification

On 2026-09-16, `go test ./internal/llm/openai -count=1` passed, including the paired
fixture and fail-closed controls. An initial incomplete-stream fixture emitted
an empty `data:` event instead of omitting `[DONE]`; correcting the fixture
confirmed the intended incomplete-stream rejection.

## Remaining live work and access boundary

This does not yet load or replay the original audit trajectory. Nor does it
capture the running agent's authenticated cloud requests. `ProcessRequest` and
`StreamProcessRequest` expose agent processing, not an HTTP interception seam
for capturing/replaying OpenAI-compatible reasoning. The inspected interface
has no existing control for this intervention.

The opt-in provider-boundary hook is now implemented (see below). Completing
the causal experiment still needs an in-process experiment driver that attaches
the diagnostic context to the authenticated provider and replays recorded tool
results. There is no remote RPC or CLI switch for it, and the running singleton
has not been rebuilt or restarted. Do not silently extract keys, change profile
endpoints, intercept TLS, restart the singleton, or run another uncontrolled
audit to work around this boundary.

Once that access is available:

1. Verify reasoning-field support and arrival for the exact deployed endpoint
   and model, rather than relying on another GLM version's documentation.
2. Capture structural metadata (model/settings, finish/usage, message order,
   tool-call IDs and hashes, reasoning arrival/replay, retries/fallback/trimming).
   Keep any raw replay material private and local; exclude authorization headers.
3. Load the pre-cycle audit history and deterministic recorded results. The
   historical reasoning may be absent; do not claim an exact preserved-history
   replay if it cannot be reconstructed. A new bounded capture may be necessary.
4. Run paired drop/preserve trials with identical history and sampling, generous
   but explicit reasoning budgets, fixed provider/model, and no real tool actions.
   Fail closed on any unrecorded tool calls or unsupported fields.
5. Repeat and reverse the intervention. Compare audit correctness and repeated
   multi-call cycles, not merely whether the endpoint emitted a final answer.

The loop guard remains independent protection. Offline test success is not a
reason to declare the live root cause fixed.


## Authenticated provider hook — implemented 2026-09-16

`internal/llm/openai/reasoning_diagnostic.go` exposes an explicit context-scoped
API. `normalizingDoer.Do` detects that context in the real `NewClient` request
path; it does not replace the provider, read keys, or require changing endpoints.
Without that context the existing transport and streaming behavior are unchanged.

Example integration inside a **bounded, deterministic experiment driver**:

```go
session, err := openai.NewReasoningDiagnostic(openai.ReasoningDiagnosticConfig{
    Mode:           openai.ReasoningPreserve, // separate session for ReasoningDrop
    BaseURL:        selectedBaseURL,
    Model:          selectedModel,
    MaxRequests:    4,
    MaxBodyBytes:   2 << 20,
    MaxMemoryBytes: 16 << 20,
    Timeout:        30 * time.Second,
})
if err != nil { return err }
defer session.Close()
ctx = openai.WithReasoningDiagnostic(ctx, session)
// Pass ctx to the EXISTING authenticated OpenAI-compatible provider.
// Set an explicit output-token limit on every model request. Replay only
// recorded tool results; do not attach a capability executor to this driver.
// After collecting the responses, inspect session.Captures() locally.
```

These are example diagnostic bounds, not recommended live reasoning budgets.
The hook bounds request count, response/request body sizes, retained payload
bytes, and elapsed session time. It does not enforce output-token budgets or
prevent tool execution above the transport layer; those remain driver duties.
`MaxMemoryBytes` counts retained payload, including the reasoning and call
metadata copies, not Go object overhead, temporary parsing buffers, or copies
returned to callers. `Close` releases the session's references, not secure heap
erasure; callers own and must dispose of their capture copies. An in-flight
request must be cancelled via its context; Close prevents it committing capture.

### Behavior and safety

- Existing authentication headers pass to the original endpoint but are never
  included in captures. Request and response **bodies can contain private data**;
  this is not a general-purpose redaction facility. No body or reasoning logging
  is introduced. Normal build structural request diagnostics still run.
- Diagnostic requests buffer complete Server-Sent Events (SSE) responses, so
  time-to-first-token measurements are invalid in this mode. Only single-choice
  streaming chat completions with one JSON data record per line are supported.
  Nonstream requests, malformed/incomplete streams, and unsupported shapes fail
  closed. Responses with nonterminal finish reasons are not proof of completion.
- Preserve mode injects exact concatenated `reasoning_content` into captured
  assistant tool-call messages. IDs, function names, argument strings, call
  order, and batch size must match the captured response. Missing reasoning,
  unknown/reused IDs, changed calls, and preexisting reasoning are rejected.
  It intentionally cannot reconstruct reasoning absent from old audit history.
- Explicit-zero temperature normalization runs before capture. Tests compare
  semantic request equality after removing only the reasoning intervention.
- Sessions are isolated; overlapping requests are rejected. The endpoint and
  model are pinned, redirects are not followed, and failed requests poison the
  session rather than silently reverting to baseline behavior. Use a fresh
  session for each trial and bypass higher-level retry/fallback orchestration.
- HTTP failures produce status-only errors and bypass raw error-body logging.
  Transport/read failures are generic; captures contain successful exchanges
  only. This hook does not implement a failure-body inspection facility.
- Captures expose actual request/response bodies plus reasoning arrival/replay
  flags and finish reason. Request settings, call IDs, and response usage (when
  supplied) are available in those bodies. Automatic hashes, trajectory loading,
  and multi-call cycle analysis are still experiment-driver work.

### Verification of the integrated hook

Passed on 2026-09-16:

```sh
go test ./internal/llm/openai ./internal/cloudfactory -count=1
go test -race ./internal/llm/openai \
  -run '^TestReasoning(Diagnostic|ContinuationDiagnostic)' -count=1
```

The real-client paired test uses an authenticated local HTTP fixture, not a
cloud endpoint. It checks exact reasoning-only intervention, preserved tool
results, isolated state, and credentials excluded from captures. Controls cover
limits, constructor validation, cancellation during body reads, concurrent use,
Close, redirects, temperature normalization, missing reasoning, fragmented call
IDs, changed tool calls, incomplete streams, and unchanged opt-out behavior.
A failing changed-call probe demonstrated the ID-only matching gap; requiring
matching call contents and batch membership fixed it before the passing runs.

No live model calls, credential extraction, agent restart, or causal GLM claim
were made as part of implementing this hook.
