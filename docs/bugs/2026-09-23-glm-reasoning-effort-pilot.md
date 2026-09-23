# GLM-5.3 low/high reasoning-effort pilot — 2026-09-23

## Question and conclusion

Does explicitly requesting `low` instead of Cercano's current `high` make bounded
GLM-5.3 implementation dispatches more productive, without increasing their
8,192-token response allowance?

**This pilot does not justify a default change.** Both settings completed all
three tasks correctly. Low used fewer reported reasoning/output tokens but did
not consistently improve first-edit or completion latency. Neither setting
reproduced the production reasoning-only, 8,192-token exhaustion failure. These
fixtures were too small to establish a remedy for that failure.

## Method

Six valid, sequential live runs through the existing authenticated DeepInfra
profile, explicitly requesting `zai-org/GLM-5.3`:

1. Shape-key reuse: high, then low.
2. Configuration precedence investigation: low, then high.
3. Line-range regression tests: high, then low.

Each arm began with identical fixture bytes at the same working path. Artifacts
were archived and the work directory reset between arms. No preceding arm's
findings were supplied. The three normalized initial HTTP request hashes match
within their pairs after removing only `reasoning_effort`.

The temporary harness uses the real `Service.RunAgenticDispatch` and cloud
provider factory/adapter. An experiment-only HTTP transport overrides the
hardcoded effort, verifies model, temperature=0, streaming and max_tokens=8192,
and preserves the remaining request fields, including reasoning replay. Every
valid request's effort was recorded at the outgoing HTTP boundary. This verifies
what was sent, not the provider's internal implementation of effort.

Controls: at most 12 HTTP requests and six minutes per run; 96,000 cumulative
reported-token dispatch budget; 48 tool calls; 32,768-token context envelope.
No compaction was wired for these small fixtures. There was no fallback provider
or retry experiment. Tool execution was confined to fixture files; commands were
restricted to Go tests/formatting under a macOS sandbox with networking disabled,
a clean environment, private-config reads denied, and a safe Go import allowlist.
This is not a full worker-RPC or large-repository reproduction.

Pre-run bounds: 6 × 12 × 8192 = 589,824 maximum requested output tokens. There is
no closed-form prediction of coding correctness; the hypothesis was lower
reasoning expenditure without losing acceptance-gate passes. A global 72-request
ceiling included the excluded preliminary run described below.

## Results

Times are dispatch elapsed time, not harness build or keychain initialization.

| Task | Effort | Correct | First successful edit | Completion | Requests | Reported reasoning tokens |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| Shape reuse | high | yes | 4.74 s | 16.02 s | 7 | 24 |
| Shape reuse | low | yes | 3.33 s | 13.42 s | 8 | 0 |
| Config precedence | low | yes | 15.95 s | 36.70 s | 11 | 27 |
| Config precedence | high | yes | 6.76 s | 24.86 s | 9 | 60 |
| Line-range tests | high | yes | 10.75 s | 23.51 s | 8 | 203 |
| Line-range tests | low | yes | 33.79 s | 42.63 s | 9 | 35 |

All six valid runs ended normally; all 52 HTTP requests succeeded. There were no
length cutoffs, timeouts, budget stops or dispatch persistence failures.

Correctness was checked independently of the model's final answer:

- Shape reuse: grade-only changes reuse geometry; each geometry field change
  invalidates it. The independent gate fails the original buggy implementation.
- Config precedence: CLI > valid environment > config > defaults, including
  invalid worker counts, independently selected fields and path resolution. The
  independent gate fails the original buggy implementation.
- Test addition: generated tests pass the correct implementation and fail all
  five compiled mutants. The original tests do not catch those mutants.
- Scope checks preserve original source where required and original test-function
  ASTs. Appending tests to an existing test file and formatting are allowed;
  deleting or modifying original test functions is not.

Aggregate valid-run usage:

| Metric | high | low |
| --- | ---: | ---: |
| Reported reasoning tokens | 287 | 62 |
| Input tokens, inclusive of cached input | 80,349 | 85,004 |
| Cached input tokens | 69,760 | 76,864 |
| Output tokens, inclusive of reasoning | 5,751 | 4,026 |
| Requests | 24 | 28 |
| Tool calls | 29 | 32 |
| Total elapsed time | 64.39 s | 92.75 s |

Low reduced reported reasoning by about 78%. Zero means none reported/surfaced;
it is not a claim about unobservable internal computation. Token counts are not
a dollar-cost comparison: cache discounts and actual billed prices matter, and
no invoice amount was collected.

## Important qualifications

- One run per task/setting is a pilot, not a statistically reliable comparison.
- Wall time contains provider noise. The first low-effort line-range response
  took 27.18 seconds despite only 17 reported output tokens and no surfaced
  reasoning. Headers arrived after 0.19 seconds. We cannot attribute that stall
  to reasoning effort or determine its exact provider-side cause.
- Consequently, the table does not establish that high is inherently faster.
- These tasks are short, explicit Go fixtures, not the large Rust/game-integration
  task that stalled in production. High's entire three-run reasoning total was
  only 287 tokens, far below a single failing production response's 8,192.
- The scope scorer initially flagged all changes to an existing test file. Review
  showed the low arm had preserved the original function and appended tests. An
  AST comparison corrected that scoring error consistently; no model rerun was
  performed to obtain the corrected score.

## Excluded preliminary trial

A preliminary high-effort shape run completed correct edits but its terminal
request failed. The sandbox wrapper returned empty text for a silent command,
unlike production RunCommand's nonempty exit-status result. That harness mismatch
can yield missing tool-message content at the provider boundary. Before running
low, the wrapper was corrected and locally tested; the preliminary result was
excluded and both valid arms used the corrected harness.

Its seven requests remain in the budget/accounting record. Total HTTP attempts
including it were 59, below the global 72-request ceiling. No failed model result
was replaced merely to improve an arm's score.

## Artifacts and next step

Private artifacts (directories 0700, files 0600):
`~/.config/cercano/experiments/glm-effort-pilot-2026-09-23/`

This contains the harness, seeds, scoring gates, per-arm resulting files,
metadata-only event logs, aggregate JSON, and the excluded-run record. API keys,
HTTP authorization headers, raw reasoning, and final model text were not saved.
Fixture-seed manifest SHA-256:
`48354909c00a4f7f2ff86e65201796f6df926991ed43ea0a9ba9c787949198d2`.

Do not switch defaults on this evidence. The next useful comparison would use a
frozen, bounded reproduction of a real stalled implementation task, preserving
its repository complexity and independently checking the requested change. A
faithful historical replay is limited by omitted opaque reasoning; use fresh
matched starts rather than claiming identical replay. No further live runs were
performed as part of this pilot.

Production defaults, configuration, and installed binaries were not changed.
