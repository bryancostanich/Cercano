# DeepInfra dispatch failures — remaining follow-ups

Investigation record, 2026-09-17. Origin: the `CERCANO - PUBLISHING`
conversation's delegations failing, plus every `zai-org/*` entry in
`~/.config/cercano/failures.jsonl`.

Three failure classes were identified from persisted sub-agent transcripts
(`conversations.db`) and the failure log. Class 1 is addressed by the GLM
reasoning work (`docs/bugs/reasoning-continuation-diagnostic.md`): hosted GLM
ran with thinking disabled by provider default, matching the degraded agentic
profile (refusals from priors, malformed Bash argv, hallucinated paths,
synthetic-summary echoes). The production client now pins `reasoning_effort`
for the GLM family and round-trips `reasoning_content` on tool-call
continuations. Whether this fully cures class 1 must be verified against real
dispatches after restart — the fix is protocol-correct but its live effect is
unmeasured.

A theory that destination-redirected cloud dispatches were getting local-style
flattened history was investigated and **retracted**: transcript `8d0447d3…`
shows 100+ native tool-call continuations on DeepInfra. The existing
`dispatch_history_test.go` coverage is valid.

## Follow-up 1: runaway context growth in sub-agent loops (FIXED 2026-09-17)

Fixed by a cumulative billed-token budget on delegated tool loops:

- `agent.ToolLoopInput.TokenBudget` sums provider-reported input+output per
  model call; the loop stops with classified `llm.ErrTokenBudgetExhausted`
  after the response that crosses the cap, without executing that response's
  tool calls. A terminal no-tool-call answer on the crossing response still
  completes — the spend already happened and the result is useful. Provider
  responses reporting no usage add nothing (no estimate-based cutoffs); the
  iteration cap remains the backstop for that path.
- `dispatch.Spec.TokenBudget`: explicit budgets pass through;
  `config.UnlimitedDispatchTokenBudget` opts out; 0 resolves the class default
  from the routing assignment's cost tier — Economy 300K, Standard 1M,
  Premium 3M (`CostTier.DispatchTokenBudget`). Main turns stay uncapped;
  every agentic dispatch (host and worker share `RunAgenticDispatch`) is
  budgeted unless explicitly opted out, including taskless legacy dispatches.
- The class is non-retryable and non-failoverable (rerunning exhausted work
  doubles the spend), and the dispatch failure message carries iterations,
  called tools, and the persisted sub-conversation ID for post-mortem.

Not done: in-loop compaction (would let long dispatches finish instead of
failing at the cap) — deliberate follow-on, informed by budgeted telemetry.
Live validation pending: no real dispatch has hit a budget yet.

## Follow-up 1 (original record): runaway context growth in sub-agent loops

Evidence: dispatch `8d0447d3…` read ~100 files sequentially with no
summarization; telemetry shows request sizes growing ~136K → ~150K input
tokens per iteration across ~25 iterations (millions of tokens billed) until
user cancellation. Nothing in the sub-agent loop bounds cumulative read volume,
and the context-window guard never trips because DeepInfra windows are huge
(≈1M for GLM-5.3), so cost — not correctness — is the failure mode.

Needed: a read/token budget for dispatched sub-agents (fail the dispatch with
a clear error when exceeded), and/or compaction inside the sub-agent loop.
Budget should be per-dispatch, configurable per task class, and enforced
host+worker. The reasoning-presence telemetry columns (`reasoning_chunks`,
`reasoning_bytes`) plus `input_tokens` per attempt give the measurement needed
to pick sane defaults.

## Follow-up 2: provider kills on large streaming requests (unfixed)

Evidence: every `operation timed out` / `connection reset by peer` against
`api.deepinfra.com` in the failure log is on requests ≥58K input tokens;
smaller requests complete. The resilience layer classifies these as transient
network errors and retries the same oversized payload, which then fails the
same way.

Needed first: reproduce and characterize the threshold (bounded experiment,
same fixture at increasing sizes) before changing retry policy. Likely
mitigations afterward: treat repeated large-payload kills as
non-retryable-as-is, surface a distinct error class, and let the follow-up-1
budget keep dispatch requests under the threshold in the first place.

## Verification state

Both follow-ups are diagnosed from persisted evidence but have no fix, no
reproduction test, and no telemetry-confirmed threshold yet. Do not close
either from this document alone.
