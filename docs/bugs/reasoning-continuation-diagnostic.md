# Reasoning continuation diagnostic — 2026-09-16

## Status: driver integrated; live cause unproven

The confirmation-gated `reasoning_diagnostic` debug tool is now wired in both
host and worker execution. See **Experiment driver** below for its input format
and invocation after restart. The original offline harness is described first.

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

The opt-in provider-boundary hook and in-process driver are now implemented
(see below). The driver is exposed through the agent capability stack, not a
new unauthenticated HTTP endpoint or a standalone key-extracting script.
Do not extract keys, change profile endpoints, intercept TLS, or run another
uncontrolled audit to work around unsupported experiment inputs.

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


## Experiment driver — 2026-09-16

Implementation: `source/server/internal/reasoningexperiment/driver.go`.
Agent entry point: `reasoning_diagnostic` (development-mode advertisement,
confirmation required even in bypass mode). Host and crash-isolated worker
both use their existing profile factory and normal credential service. The
selected **named profile** and explicit model are fixed for the entire pair;
profile backups, routing redirects, retries, and compaction are deliberately
not used. Local-only policy rejects the experiment before credential access.
There is no model-requested tool executor in this package.

### After rebuilding and restarting

1. Copy `docs/bugs/reasoning-experiment.example.json` to a private local file.
   Set `profile` to an existing chat-completions profile and `model` to its exact
   deployed model ID. Keep the profile's configured endpoint unchanged.
2. Supply the initial text history, tool schemas, and deterministic recorded
   results. This is explicit input preparation, not an automatic database read.
   The example is a smoke probe, **not the original audit trajectory**.
3. Invoke the agent tool and approve the paid experiment:

   ```json
   {"input_path": "/absolute/path/to/private-experiment.json"}
   ```

4. Repeat with `preserve_first: true` to reverse arm order. Each invocation has
   fresh, isolated capture sessions. No live trials run during build or restart.

Input uses the JSON shape in the example (`messages` use the existing block
format). Unknown input fields and trailing documents are rejected. Recorded
arguments must be JSON objects: key ordering/whitespace do not affect lookup,
but names and values must match. Duplicate recorded actions are rejected rather
than guessing which result to replay. Unknown calls stop that arm without
executing anything. Generated IDs are attached to the corresponding recorded
result; no actual `Read`, `Bash`, write, or other capability is invoked.

### Explicit bounds

- 1–12 requests per arm, 1–32768 requested output tokens per request, at most
  262144 requested output tokens across the pair. The upper bound is
  `2 × max_requests × max_tokens`; it is not a dollar-price estimate or an
  assertion that a remote provider honors token limits.
- One shared 1–600-second request deadline for the pair. Credential lookup uses
  the existing host/worker credential path, not an independently killable process.
- At most 8 MiB of input, 64 tool definitions and 1024 recorded actions.
  Replay growth has a conservative 8 MiB encoding budget (including an allowance
  for JSON escaping) checked before constructing the next provider request.
- Hook limits: 8 MiB per HTTP body and 64 MiB of retained payload per arm.
  These are payload limits, not a hard process-resident-memory guarantee.
- At most 64 call IDs per response, each at most 256 bytes in the report.
- No output files, raw reasoning logs, or automatic raw capture persistence.
  Captures are released after each arm; the input file remains caller-owned.

### Reading the report

The report contains arm order, statuses, request/response SHA-256 hashes,
reasoning arrival/replay flags, tool IDs, action/result batch hashes, finish
reasons, served model when supplied, and usage with explicit known/unknown
flags. It never returns raw prompts, tool results, answers, reasoning, HTTP
headers, or credentials. Hashes are fingerprints, **not anonymization**.
Existing adapter logs still include structural request diagnostics.

`comparable_inputs[i]` compares same-index request bodies after removing only
`reasoning_content`. Different generated call IDs also make it false. Live
responses are independently sampled; identical initial inputs and temperature
zero do not guarantee identical trajectories. Never interpret a divergent pair
as a controlled reasoning-only causal result.

`terminal_stop` records a terminal provider finish, **not audit correctness**.
`repeated_action_result_batch` observes an identical ordered action/result batch
at any earlier step, ignoring call IDs; this detects A→B→A as well as immediate
repetition but does not prove an infinite loop. Budget exhaustion, unrecorded
calls, nonterminal finishes, timeout/cancellation, and capture/provider failures
remain distinct outcomes. Raw provider errors are not included in the report.

Initial history must be text-only. Historical tool messages without their
original captured reasoning cannot seed a valid preserve arm; those inputs are
rejected before network access. The driver collects new reasoning from the first
response. It does **not** reconstruct the old audit, assess answer correctness,
or establish the live cause of repeated investigations. Those are experiment
interpretation/input-preparation tasks, not reasons to modify production
reasoning behavior based on the scripted tests.

### Driver verification

Local authenticated HTTP fixtures verify paired reasoning-only requests, both
arm orders, fresh-ID repetition and multi-step cycles, deterministic replay,
unknown-tool rejection, budgets, cancellation, malformed streams, missing
reasoning, and report privacy. Host and worker integration tests verify normal
credential use and no backup requests; permission/catalog tests verify debug
advertisement and confirmation. The tests never call a live cloud model.


Final driver gate passed on 2026-09-16:

```sh
go test ./internal/reasoningexperiment ./internal/llm/openai \
  ./internal/capabilities/builtins ./internal/toolstack \
  ./internal/hostsvc/providers ./internal/server ./internal/worker \
  ./internal/agent -count=1
go test -race ./internal/reasoningexperiment ./internal/llm/openai \
  ./internal/hostsvc/providers ./internal/worker ./internal/agent \
  ./internal/capabilities/builtins \
  -run 'Test(PairRealTransport|RejectBeforeBuild|FailClosedOutcomes|DecodeStrict|RepeatedMultiStepCycleAndReplayMemory|PairDeadline|ReasoningDiagnostic|WorkerReasoningDiagnostic)' -count=1
go vet ./internal/reasoningexperiment ./internal/capabilities/... \
  ./internal/toolstack ./internal/hostsvc/providers ./internal/worker \
  ./internal/server ./internal/agent
make build
```

The binary was Developer ID signed. No agent restart or live inference was
performed. An earlier broad run hit `TestAttachConversation_TwoSurfacesSeeOneTurn`
(received two rather than three events); ten isolated reruns and subsequent
full server and final package runs passed without modifying that test. The
new timeout fixture initially hung during server cleanup because its handler
had not consumed the request body; consuming it let the server observe client
cancellation. A separate failing named-pipe probe confirmed that ordinary
`os.Open` could block before input validation; Unix input now uses a nonblocking
open followed by a regular-file check, and the probe passes.


## Audit preparation — 2026-09-16 (second session)

The first live smoke pair (DeepInfra `zai-org/GLM-5.3`, fixture prompt) reached
the endpoint through the confirmation-gated tool: the drop arm called the
recorded tool once and stopped; the preserve arm received **no**
`reasoning_content` on its first response, so the driver failed closed without
a continuation. That pair validated plumbing only — wrong prompt shape for the
bug and no reasoning to replay — and is not evidence about the audit loop.

Changes made for a real experiment (committed with this note):

- `ReasoningEvidence` on each capture/step distinguishes absent, null, empty
  and nonempty `reasoning_content` chunks with counts/bytes, separate from the
  replay boolean. Wire presence only; no reasoning text is reported.
- Optional `reasoning_effort` (`none|low|medium|high`) pins that field on every
  diagnostic request. DeepInfra documents it for chain-of-thought models
  (docs.deepinfra.com/chat/reasoning; the GLM-5.3 model card documents
  `low|high|max` with a `max` default — an unresolved discrepancy; unsupported
  values are model-defaulted per z.ai). Diagnostic-only: normal client requests
  are unchanged, and a conflicting caller-set value fails the session.
- `baseline_only` runs a single drop arm (halved token exposure) for cheap
  cycle-reproduction attempts before any paired preservation trial.
- `source/server/scripts/prepare-reasoning-audit.py` (offline, read-only)
  extracted the original audit `e0e7e7a3eac3775c38e11713` from
  `conversations.db` into `~/.config/cercano/reasoning-experiment-e0e7e7a3/`
  (0600, outside the repo): 151 turns, 75 batches, 81 recorded actions, and a
  real detected cycle — period 6 batches starting at batch 43 with 5 complete
  repetitions. One `Grep` action returned 4 result orderings (same length);
  replay canonicalizes each action to its earliest recorded result. The seed is
  a text-transcoded prefix (original system prompt, wire settings and
  historical reasoning unavailable), so this is a fresh bounded capture, NOT an
  exact historical replay; preserve arms use newly captured reasoning.

Baseline gate (explicit): run `baseline.json` (12 requests × 8192 tokens,
single drop arm) first. Only if the drop baseline reproduces a repeated
action/result cycle AND nonempty reasoning arrives should the paired
preserve experiment run. If the baseline does not reproduce, the missing
pieces are the untransportable historical context — do not keep paying for
repetitions of a non-reproducing fixture.

Status: prepared but NOT run. The running agent predates `baseline_only` and
`reasoning_effort` (strict input decoding would reject the fixture), so the
rebuilt binary must be restarted into before the baseline attempt.


## Baseline outcome and live evidence pivot — 2026-09-16

The prepared audit baseline ran once (drop arm, `deepinfra` /
`zai-org/GLM-5.3`, `reasoning_effort: high`, ~101K input tokens): reasoning
arrived properly (731 nonempty chunks, 8,383 bytes — confirming the smoke
pair's missing reasoning was a settings/prompt artifact, not a transport bug),
but the model made zero tool calls and finished `stop` on the first response.
The recorded cycle did NOT reproduce. Per the baseline gate, no paired trial
was run and no further replay spending is planned: the loop evidently depended
on untransportable context (original system prompt, wire settings, historical
reasoning), so the retroactive causal question is closed as unprovable with
the data we retain. The offline mechanism finding stands; the live cause
remains unproven.

### Forward-looking wire evidence (implemented)

So the next suspected loop carries its own data, every normal OpenAI-compatible
call now records adapter-measured reasoning **wire presence** — no reasoning
text, no behavior change:

- `llm.TokenUsage.ReasoningChunks/ReasoningBytes`: nonempty `reasoning_content`
  delta count and total UTF-8 bytes for one attempt. Known-zero means the
  adapter confirmed absence; unknown means the adapter doesn't measure (other
  adapters unchanged for now). Observations, never summed into token costs.
- Streaming counts every nonempty delta (evidence is still recorded when the
  buffered reasoning is later promoted to visible text); non-streaming records
  0/1 chunks from the completion message. Values ride the existing terminal
  usage event, `usage.Attempt` accounting, the worker wire batch
  (proto fields 21/22), and two additive nullable `inference_attempts` columns
  (`reasoning_chunks`, `reasoning_bytes`) with an idempotent duplicate-tolerant
  migration. Legacy rows stay NULL rather than inventing confirmed absence.
- Query per-conversation presence with `sqlite3
  ~/.config/cercano/telemetry.db "SELECT model, outcome, reasoning_chunks,
  reasoning_bytes FROM inference_attempts WHERE conversation_id=? ORDER BY
  started_at"` — a repetition loop whose attempts show `reasoning_chunks=0`
  under a model that normally reasons is the signature this exists to catch.

Found while verifying: `TestCollectorOwnsAccountingLane` failed on clean HEAD
before these changes — the telemetry store opened SQLite without a busy
timeout, so concurrent legacy-event and accounting writers hit immediate
SQLITE_BUSY drops (the logged `failed to record event: database is locked`).
Fixed by carrying `busy_timeout(5000)` in the DSN so every pooled connection
waits instead of dropping; the package now passes 10 consecutive runs.

Verified: llm (all adapters), telemetry (×10), usage, worker,
reasoningexperiment, server, agent, runner, hostsvc/... package tests;
focused race runs on the openai/usage presence tests; proto regenerated;
go vet; signed make build. Restart required to run the new binary.


## Production fix: GLM effort pin + reasoning round-trip — 2026-09-17

Investigating the CERCANO-PUBLISHING dispatch failures showed hosted GLM
dispatches degrading exactly as a no-thinking profile predicts (refusals from
priors, malformed tool calls, synthetic-summary echoes), while the diagnostic
had already proven DeepInfra serves zero reasoning until the effort field is
pinned. The production chat_completions client now:

- pins `reasoning_effort: high` for GLM-family models (`glm-*` basename) on
  non-llama-server backends; `DisableThinking` surfaces (e.g. watchdog) win
  and remain reasoning-free. Other models' requests are byte-identical.
- captures `reasoning_content` that accompanies tool calls as an opaque
  `BlockReasoning` round-trip block (streaming and non-streaming), and the
  adapter returns it verbatim on the continuation's assistant tool-call
  message, per z.ai's protocol. Terminal no-tool-call answers keep the
  existing promote-to-text recovery. Non-GLM and local paths are unchanged.
- Responses-flavor replay now requires a reasoning item ID, so chat-captured
  plaintext state can never be sent to the Responses API.
- the experiment driver strips production-captured reasoning from its built
  history so diagnostic sessions remain the only intervention (guarded by
  `TestPairWithGLMProductionCaptureStaysIsolated`).

Caveats: reasoning tokens are now billed on every GLM cloud call; the pinned
`high` is the overlap of DeepInfra's documented values and the GLM card's —
the card's `max` is not sent. Live effect on dispatch quality is unmeasured
until real dispatches run; the presence columns in telemetry.db are the
measurement. Remaining non-reasoning failure classes (context runaway, large
payload provider kills) are tracked in docs/bugs/deepinfra-dispatch-followups.md.

Verified: family gating table; effort pin + round-trip + isolation gates
(cloud GLM / flash / DisableThinking / non-GLM / llama-server); replay lands
only on the assistant tool-call turn, exactly once; terminal promotion
regression; non-streaming mirror; presence accounting agreement; full llm,
agent, worker, runner, trajectory, reasoningexperiment suites; focused race
runs; go vet; signed make build.
