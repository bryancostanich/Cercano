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
  Premium 10M (`CostTier.DispatchTokenBudget`). Main turns stay uncapped;
  every agentic dispatch (host and worker share `RunAgenticDispatch`) is
  budgeted unless explicitly opted out, including taskless legacy dispatches.
- The class is non-retryable and non-failoverable (rerunning exhausted work
  doubles the spend), and the dispatch failure message carries iterations,
  called tools, and the persisted sub-conversation ID for post-mortem.

Premium raised 3M → 10M (2026-09-25). The cumulative sum is dominated by
resent history rather than work produced: a measured implementation dispatch
billed 2,780,649 input against 82,942 output (97% input, ~34x the cap per
unique generated token). Because every turn resends the conversation, the cap
behaves as a turn ceiling independent of productivity — at ~37K mean input the
3M ceiling bound that dispatch at 74 turns and stopped it mid-implementation
with its plan still in context. 10M leaves room to finish and verify a
substantial refactor while still tripping well before an unbounded loop runs
unattended. Economy and Standard are unchanged. Note that the budget counts
`input + output` including cache reads where the provider reports them; the
measured run reported no cache fields, so the discounted share is unknown.

In-loop compaction now complements the budget (2026-09-17). Sub-agent
dispatches never had compaction at all: main turns compact asynchronously via
the store-backed compactiongen.Generator (~10s debounce, multi-minute budget),
but sub-agent history lives only in memory, is never read back, and a dispatch
usually finishes before a debounced background pass would even start.

internal/loopcompact runs the SAME algorithm (compactor.Advance), the SAME
config (cfg.Compaction) and the SAME local fast_light_text summarizer the main
loop uses — only synchronously, between iterations, once history crosses the
40K-token activation floor. Measured on a 43,680-token fixture: 43,680 -> 12,766
tokens (71% reduction). Below the floor — the common case for short dispatches
— it is a strict no-op that never calls the summarizer.

Design points worth keeping:
- agent.LoopCompactor is a callback seam because internal/compactor imports
  internal/agent (BuildLLMHistory), so the loop cannot import it directly.
- Compaction is defensive: any error, nil, or empty result leaves history
  untouched, and results are pairing-repaired so a compacted view can never
  orphan a tool_use. Compaction is an optimization, never a reason to fail
  work already paid for.
- Summarizer spend is charged to the dispatch token budget (even on failure),
  so compaction cannot quietly erode the cap — and a pathological summarizer
  can itself exhaust the budget rather than grind indefinitely.
- Turn timestamps fed to Advance are synthetic and monotonic; real wall-clock
  stamps cluster tool bursts into one second and stall the frozen boundary.
- One compactor per dispatch: frozen summary state must not leak between
  concurrent sub-agents.

Still not done: worker-side wiring. The factory is installed in the host front
door (cmd/cercano) where the summarizer and compaction config exist; worker
processes have no local runtime handle, so dispatches executing there still
run uncompacted. Live validation pending: no real dispatch has yet crossed
either the budget or the activation floor in production.

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
