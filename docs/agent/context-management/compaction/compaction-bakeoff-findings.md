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

## Run 3 — real session (few, large messages → 5 segments)

`-maxtokens 48000 -segtokens 8000 -verbatim 6` → **36 messages / ~46k tokens /
5 segments**. All pairing-valid; reduction ~63–64% across all three.

| contender | summary content (lines) | read |
|---|---|---|
| rolling | 17 | coherent — **did not collapse** at 5 segments |
| B mechanical | 30 | richest, redundant |
| C model | 21 | tightest coherent — best |

Key point: with only **5 generations**, rolling held up (non-empty, coherent).
Contrast Run 2's 9 segments where it collapsed. C still the tidiest; B the most
redundant.

## Run 4 — real session (44 msgs → 9 segments)

`-maxtokens 80000 -segtokens 8000 -verbatim 6` → **44 messages / ~80k tokens /
9 segments**. All pairing-valid; reduction ~78–80%.

| contender | summary content (lines) | read |
|---|---|---|
| rolling | 23 | eroded (leanest, lost detail) — did NOT fully collapse here |
| B mechanical | 48 | heavy redundancy (grows with segment count) |
| C model | 30 | consolidated B's 48→30, coherent — best |

Note: rolling at 9 segments survived here but collapsed in Run 2 — its failure
is **stochastic** (local-model variance), but the erosion trend is consistent
(rolling always carries the least content). B's redundancy **grows with segment
count** (30 lines @5 seg → 48 @9 seg); C reconciles it back down each time.

## Conclusion (3 real sessions)

- **Winner: C (map-reduce / model reduce).** Consistently the tidiest *coherent*
  summary — keeps the substance, dedupes B's sprawl, right-sized regardless of
  segment count. Costs one extra model call per run; clearly worth it.
- **B (map-reduce / mechanical):** complete but redundancy compounds with length;
  viable fallback, and notably C's reduce pass is exactly what fixes it.
- **Rolling: disqualified.** Erodes monotonically with generations and can
  collapse to an empty summary (Run 2). Unreliable for long sessions.
- The synthetic-corpus verdict (Run 1) was inverted by real data — small
  fixtures couldn't exercise erosion or accumulation. **Real-session testing was
  decisive.**

**Decision for 2b: wire C (map-reduce / model reduce) as the compactor**, with
frozen-segment caching layered on top (C maps each segment once; freezing those
maps is the natural cost optimization).

---

# Frames matrix (second series, 2026-07)

Separate experiment series from the map-reduce bakeoff above. Compares the five
survey frames (A rolling baseline, B adaptive, C elision-first, D extractive,
E retrieval-backed) over a stored conversation window: conv `80109e871fba4e18`
around turn `adfada03…` (the models×tiers design proposal), 40 before / 10
after, ~11.3k tokens, 7 must-keep anchors, model `qwen3-coder-next:latest`.
Harness: `compaction-bakeoff -conv` matrix mode.

## Runs 1–3 — default temperature (3 reps each, 9 samples/frame)

| frame | anchors range | reduction |
|---|---|---|
| rolling | 4/7 – 7/7 | ~88% |
| adaptive | 0/7 – 7/7 | ~68% |
| elision-first | 7/7 always | 13% always |
| extractive | 0/7 – 7/7 | ~87% |
| retrieval(rolling) | 5/7 | ~79% |

**Sampling variance dominated every LLM frame** — identical input swung scores
from worst to perfect between reps. Two harness fixes landed during this span:
unwrap model quote-wrapping before the grounding check (run 3), and add the
baseline's dedup rule to the extractive prompt (run 4).

## Run 4 — greedy decoding (temperature 0, 2 reps)

| frame | anchors | grounded | reduction |
|---|---|---|---|
| rolling | 5/7 | — | 88% |
| adaptive | 5/7 | — | 68% |
| elision-first | 7/7 | — | 13% |
| extractive | 0/7 | 3/6 | 87% |
| retrieval(rolling) | 5/7 | — | 78% |

**Every frame reproduced exactly across reps** (rep 1 == rep 2 on all scores).
The run-1–3 spread was pure sampling noise; temperature 0 removes it.

Findings:

- **Rolling at temp 0 kept all five design-proposal anchors** (`most_capable`,
  `fast_light`, `models.Resolve`, `default_provider`, `tier`) — the two misses
  were the incidental model tags (`claude-haiku-4-5-20251001`, `phi4`). The
  fidelity guardrails + greedy decoding preserve the load-bearing proposal
  deterministically.
- **Extractive is deterministic-bad at temp 0**: it fixates on the dominant
  recent topic (tool-click debugging), quotes large code blocks verbatim
  (burning budget), and skips the design proposal entirely — in *both* map
  segments, including the one containing the proposal. Greedy decoding locks
  this failure in.
- **Quote fidelity cannot be prompted into this model**: even quote-only
  instructions at temp 0 yield 3/6 grounded bullets — half are paraphrases.
  Frame D is not viable with qwen3-coder-next.
- **Elision-first stays the only 7/7 frame** but its 13% reduction does no real
  compaction work; it is a floor, not a compactor.

## Conclusions for production

1. **Run the summarizer at temperature 0.** Production compaction currently
   samples at default temperature — the single highest-leverage change from
   this series. Makes compaction reproducible and keeps the proposal anchors.
2. **Keep the deterministic elision floor + verbatim tail** under whatever
   summarizer runs above it (composition already in place).
3. **Drop frame D (extractive)** for this model class; revisit only with a
   model that can actually quote.
4. Adaptive's lower reduction (68%) bought no anchor advantage over rolling —
   no reason to switch the prompt shape on this evidence.

---

# Summarizer model selection (fast_light_text tier), 2026-07-05

The compaction summarizer now resolves its model as: explicit
`compaction.summarizer_model` → `models.tiers.fast_light_text.open` → the
interactive open model. Populating the tier is how you switch the summarizer;
this section records the audition method and the first result.

## Audition method

Production-path check on the tiers-proposal window (conv `80109e871fba4e18`
around turn `adfada03…`), greedy decoding, five must-keep anchors plus
fabrication tells:

```
compaction-repro -conv 80109e871fba4e18 -aroundturn adfada03679f33c3a13fa50a \
  -before 40 -after 10 -model <tag> \
  -anchors 'most_capable,fast_light,models.Resolve,default_provider,tier'
```

**Bar to clear: 5/5 anchors, all tells clean.** Anything less does not get to
summarize history — a missed anchor here is a dropped design decision in
production.

## Results

| model | anchors | time (2 calls) |
|---|---|---|
| qwen3-coder-next (current) | 5/5 | ~30 s |
| phi4:14b | 4/5 | ~55 s |

phi4 dropped `models.Resolve` — a function-signature anchor, exactly the
load-bearing identifier class the PROPOSALS/fidelity work protects — and was
slower despite being 5× smaller on disk: qwen3-coder-next is sparse
mixture-of-experts, so parameter count on disk is a poor latency predictor.
Part of phi4's 55 s is cold-load, but there is no fidelity-neutral speed win.

## Standing decision

- `fast_light_text.open` stays **unset**; the summarizer stays on the
  interactive open model (qwen3-coder-next), the best-measured option.
- Candidates for the tier (e.g. a 4B-class text model) must pass the audition
  above before being configured.
- Dense-vs-MoE lesson: judge summarizer candidates on measured latency and
  anchor retention, never on parameter size.
