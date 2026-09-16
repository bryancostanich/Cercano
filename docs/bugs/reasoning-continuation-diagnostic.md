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

Completing the causal experiment therefore needs a separately integrated,
opt-in provider-boundary hook in an authenticated agent process (and a rebuilt
process to use it), or another explicitly approved authenticated test route.
Do not silently extract keys, change profile endpoints, intercept TLS, restart
the singleton, or run another uncontrolled audit to work around this boundary.

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
