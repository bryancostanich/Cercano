# Compaction Bake-off — Findings

Running log of the compaction algorithm bake-off (2a). Records metrics and
**qualitative verdicts only** — never the raw session content (real sessions
carry private project detail). Reproduce with `cmd/compaction-bakeoff`.

Contenders: **rolling** (sequential fold), **B = map-reduce/mechanical**
(per-segment summarize + deterministic `MergeSummaries`), **C =
map-reduce/model** (per-segment summarize + a model reduce pass).

## Run 1 — synthetic corpus

`compaction-bakeoff -addr localhost:50052` (segment budget 40, sized to the
tiny fixtures). Local model: `qwen3-coder-next` via the agent.

- All 15 rows **pairing-valid**; bake-off infrastructure validated end-to-end
  against the real agent path.
- **Anchor retention identical** across all three (e.g. 3/3, 5/5, 4/4; all three
  miss the same 1 fact on research-fetches → 3/4). The quality dimension never
  got exercised.
- Reduction differentiated weakly; several **negative** (summary preamble +
  verbatim window outweighs savings on tiny conversations — a regime you would
  never trigger compaction in).
- Apparent verdict: B a weak winner; C added a model call with no benefit.

**Caveat that turned out to matter:** the fixtures are too small for (a) rolling
to compound-fail or (b) B's accumulation to need reconciling. So this verdict was
**misleading** — see Run 2.

## Run 2 — real session (chip-design/viz work)

`-transcript <session> -maxtokens 64000 -segtokens 8000 -verbatim 6`.
Sliced to **139 messages / ~63k tokens / ~9 segments**. All pairing-valid.

| contender | reduction | calls | final summary |
|---|---|---|---|
| rolling | 96% | 9 | **EMPTY** — collapsed to no content |
| B map-reduce/mechanical | 93% | 9 | comprehensive but sprawling (~25 decisions, heavy redundancy) |
| C map-reduce/model | 95% | 10 | tight, deduplicated, coherent — **best** |

What happened:

- **Rolling collapsed.** Nine sequential re-summarization generations eroded the
  running summary to *nothing* (`[conversation summary]` with empty sections).
  This is the predicted compounding-loss failure — and on real data it's
  catastrophic, not subtle. Rolling is **disqualified**.
- **B kept everything but got noisy.** `MergeSummaries` accumulates each
  segment's summary, so nothing is lost — but the same decision recurs ~3× in
  slightly different words across ~25 decisions. Complete, unwieldy.
- **C won.** The extra model reduce pass reconciled B's accumulation into a
  crisp goal, ~9 deduplicated decisions, a clean file list, and prioritized open
  threads. The reduce call earned its cost precisely by taming the accumulation
  B could not.

**Verdict flipped vs Run 1.** On tiny synthetic fixtures C looked worst; on a
real long session **C is clearly best and rolling is out**. Reduction (93–96%)
does not differentiate at scale — the *summary content* does, which the
side-by-side view surfaces. This is exactly why real-session testing was needed.

Caveats: one session; rolling's *fully empty* result is partly a local-model
artifact (re-summarization returning empty sections), though the structural
erosion is real regardless.

## Provisional conclusion

- **Winner: C (map-reduce / model reduce).** Drop rolling.
- Confirm across 2–3 more real sessions (below) before locking C in for 2b.

## Run 3 — real session

_(pending)_

## Run 4 — real session

_(pending)_
