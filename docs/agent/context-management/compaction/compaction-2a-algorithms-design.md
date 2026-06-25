# Compaction 2a (part 2) — Summarizer + Algorithms + Bake-off — Design

**Status:** Design approved. Implementation not started.

The second half of compaction 2a. Part 1 built the deterministic foundation
(the `Compactor` interface, segmentation, mechanical elision, structured-summary
merge/render, send-view assembly, the fixture corpus, and the metrics harness)
proven with an elision-only baseline. Part 2 adds the **real summarizer
contract**, the **model-backed algorithms** that compete in the bake-off, and the
**standalone runner** that scores them against a real model so we can pick a
winner.

## Why

The bake-off's whole purpose is to choose a summarization algorithm by
measurement, not argument. That needs (1) a way for the local/cloud model to
return a parseable `StructuredSummary`, (2) the candidate algorithms implementing
the part-1 `Compactor` interface, and (3) a way to run them against a real model.
The differentiator between candidates is *summary quality* — which is fuzzy and
model-dependent — so the quality run is deliberately separated from the
deterministic CI suite.

## Scope boundary

- **In:** the `SummarizeFunc` contract (section-tagged format + lenient parser),
  the agent-backed summarizer, the three contender `Compactor`s, their
  deterministic structure tests, and the standalone quality bake-off.
- **Out (→ 2b, the live-wiring spec):** the background trigger, the persisted
  derived layer, retention enforcement, `/c` integration, and **frozen-segment**
  (see below).

## 1. The summarizer contract

The local model is plain text in / text out (`Process(Request{Input}) →
Response.Output`); there is no JSON mode. The codebase already parses structured
fields from free-form model text (`research/planner.go`). So the summarizer uses
a **section-tagged format with a lenient parser**, not JSON.

**Prompt → labelled sections.** The summarization prompt asks the model to emit
fixed labelled sections:

```
GOAL: <one line>
DECISIONS:
- <decision>
- <decision>
FILES:
- <path>: <latest state>
OPEN:
- <open thread>
STATE: <one line>
```

**Lenient parser.** `ParseSummary(text string) StructuredSummary` extracts each
section by its label, tolerant of:
- missing sections (absent → empty field, never a hard failure),
- extra prose before/after the block (e.g. a model preamble),
- markdown fences or bullets in varied forms (`-`, `*`, `1.`).

A malformed `FILES:` block costs only that one field. The parser is pure and
**deterministically unit-tested** (well-formed, missing-section, prose-wrapped,
and garbage inputs).

`SummarizeFunc` (defined in part 1) is implemented by composing: build the
prompt for the given messages → call the model → `ParseSummary` the response.

## 2. The agent-backed summarizer

The summarizer backend is the **cercano agent**, not a raw local provider. The
runner connects to a running agent via `agentclient.Dial(addr)` and, per
summarization call, sends the prompt through `StreamChat`, accumulates the
streamed response text, and parses it.

This matters for two reasons:
- **Model choice isn't limited to local.** The agent routes to whatever model
  it's configured for (cloud or local), so the bake-off can validate each
  algorithm's true quality with a strong model *and* characterize the degraded
  local case by pointing the agent at local.
- **It exercises the real path 2b will use** — the agent handling a summarization
  prompt and returning findings. If the agent mangles the prompt (adds preamble,
  attempts a tool), we learn it now, from the parser's behavior.

The agent-backed `SummarizeFunc` lives only in the runner (it needs a live
connection); the algorithms and CI tests never touch it (they use a fake).

## 3. The three contenders

All three implement the part-1 `Compactor` interface and share one pipeline,
differing only in the summarize/reduce middle:

```
raw → ElideSupersededToolResults (part 1, mechanical)
    → split: [older history] + [recent verbatim window (Budget.VerbatimRecent)]
    → summarize the older history per the algorithm → StructuredSummary
    → AssembleSendView(summary, recentVerbatim)   (part 1; ends in RepairPairing)
```

- **A — Rolling.** Segment the older history; fold sequentially:
  `summary₀ = summarize(seg₀)`; `summaryₙ = summarize(render(summaryₙ₋₁) ++ segₙ)`.
  One model call per segment, carrying the running summary forward. Exhibits
  compounding loss and recency bias — the baseline the others must beat.
- **B — Map-reduce, mechanical reduce.** Segment; `summarize(segᵢ)` each from raw
  (independent, no compounding); combine with the deterministic part-1
  `MergeSummaries`. One model call per segment, zero reduce-pass loss.
- **C — Map-reduce, model reduce.** Same per-segment map as B, then a **second
  model pass** reconciles all segment summaries into one (render them, ask the
  model to merge/dedupe). Better semantic dedup in principle; costs one extra
  model call and incurs its own summarization loss.

A (sequential) vs B/C (parallel-map) answers "does compounding loss hurt?";
B vs C answers "is the model reduce pass worth its cost?".

## 4. Frozen-segment is deferred to 2b (rationale)

In a single `Compact()` over a fixed history, **frozen-segment is byte-identical
to map-reduce** — segment, map each from raw, reduce. Its only distinguishing
move is *freezing each segment summary so later turns never re-summarize it*,
which is a cross-turn **cost** optimization, invisible in a stateless one-shot
call and unmeasurable by a quality metric (its quality equals map-reduce's by
construction). So frozen-segment belongs with the live, stateful work in 2b,
judged on recompute savings — not in this single-shot quality bake-off.

## 5. The bake-off runner

A **standalone `cmd/compaction-bakeoff`** program — NOT part of the test suite.
It is run once to validate the design, and again only if an algorithm changes.

- Connects to a running agent (`agentclient.Dial`, address via a flag),
  builds the agent-backed `SummarizeFunc`.
- For each contender × each corpus fixture, runs the part-1 `Score` harness and
  prints a metrics table: token reduction, anchor retention %, dedup ratio,
  pairing validity, and model-call count.
- Exits non-zero if any produced send-view is pairing-invalid (a hard
  correctness floor independent of quality).

The output table is the artifact the human reads to pick the winning algorithm.

## 6. Corpus additions

Part 1 shipped three fixtures (repeated-reads, refactor-many-files, light-qa).
Part 2 adds the fixtures whose value is anchor-retention *under summarization*
(part 1's elision baseline couldn't differentiate them):
- **long-debug** — a long debugging session that revisits a hypothesis across
  many turns; tests whether the goal and the final root-cause survive.
- **research-fetches** — many web fetches with distinct findings; tests whether
  distinct facts are retained vs. blurred together.

Each declares its `MustKeep` anchors, like the part-1 fixtures.

## 7. Testing

**Deterministic CI tests (no model):**
- `ParseSummary` — well-formed, missing-section, prose-wrapped, and garbage
  inputs map to the expected `StructuredSummary`.
- Each contender's **structure**, driven by a **fake `SummarizeFunc`** that
  returns a recognizable per-input summary: assert Rolling threads the prior
  summary into each step (the fake records the prompts it sees), B maps each
  segment independently then merges, C performs the extra reduce pass. Assert all
  three emit a pairing-valid send-view and keep the recent window verbatim.

**Standalone quality run (real model, not in CI):**
- `cmd/compaction-bakeoff` against a running agent — the only model-dependent,
  non-deterministic piece.

## Error / edge

| Case | Behavior |
|---|---|
| Model returns no recognizable sections | `ParseSummary` returns an empty summary; `AssembleSendView` then adds no preamble — the send-view is the (elided) body, still valid |
| A segment summary fails (agent error) | Algorithm propagates the error; the runner records the failure for that fixture and continues others |
| Recent window ≥ whole history | Nothing to summarize; send-view = elided verbatim history |
| Agent attempts a tool / adds preamble | Lenient parser ignores non-section prose; learned and reported by the run |
| Send-view pairing-invalid | Cannot happen (assembly ends in `RepairPairing`); runner asserts it and fails loudly if so |

## Out of scope

Frozen-segment (2b); the live background trigger, persistence, retention, and
`/c` integration (2b); JSON output mode; cross-conversation memory; picking the
winner before the run produces numbers.

## Key file references

| Concern | Location |
|---|---|
| Compactor interface, Budget, Result, SummarizeFunc, Segment | `source/server/internal/compaction/types.go` (part 1) |
| Elision, segmentation, merge, send-view, Score/Metrics, Corpus | `source/server/internal/compaction/` (part 1) |
| New: parser + summarizer | `source/server/internal/compaction/summarizer.go` (`ParseSummary`, prompt builder) |
| New: algorithms | `source/server/internal/compaction/rolling.go`, `mapreduce.go` |
| New: corpus additions | `source/server/internal/compaction/corpus.go` (extend) |
| New: runner | `source/server/cmd/compaction-bakeoff/` |
| Agent connection / streaming | `source/server/pkg/agentclient/client.go` (`Dial`, `StreamChat`) |
