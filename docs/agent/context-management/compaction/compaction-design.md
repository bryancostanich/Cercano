# Background Auto-Compaction — Design

**Status:** Design approved. Implementation not started.

Compacts a conversation's context in the background so the agent keeps sending a
small, high-signal history to the cloud model while the raw original stays
durable and cold for inspection/undo. Because summary quality is the hard part
and is fuzzy, the algorithm is **chosen by measurement**, not by argument: we
build a pluggable compactor, a documented corpus, and a metrics harness, then
run a bake-off.

## Why

Cloud context is ~1M tokens; the local compaction model is ~128k. A long session
will exceed both the local window (so summarization must work in chunks) and the
useful cloud budget (so we must reduce what we send). Most of the bytes are stale
**tool results** — file reads, search dumps, build/test logs — that the model
rarely needs again. We want to reclaim that automatically, in the background,
without losing the thread of the work and without sending the cloud an invalid
message array.

## Build split (measure-first)

- **2a — Measurement infrastructure.** The `Compactor` interface, the shared
  substrate, the candidate algorithms, the corpus, and the metrics harness.
  Mutates **no** live state; reads raw fixtures, emits derived views, scores
  them. Output: a measured winner.
- **2b — Live wiring.** Wire the winning compactor into the agent: the
  background trigger, the persisted derived layer, retention enforcement, and
  `/c` integration.

2b's shape depends on 2a's result, so 2a ships and is measured first.

## Layering — agent-owned, client-agnostic (hard constraint)

**All compaction logic lives in the agent (server) layer, never in a client.**
The CLI is a thin gRPC consumer; VS Code, Zed, and any MCP consumer must get the
same functionality through the same surface. Concretely:

- The `Compactor`, the shared substrate, segmentation, the derived-layer
  persistence, the background trigger, and retention enforcement all live under
  `source/server/internal/...` (e.g. a new `compaction` package), beside `recap`
  and `conversation`. No client reimplements any of it.
- The agent **service interface** (`source/proto/agent.proto`) is the only way
  clients touch it, following the existing context-operation RPCs
  (`ProposeContextEdit`, `DeleteConversationTurns`, `GetConversationTurns`,
  `GetContextUsage`). New RPCs (named in 2b) cover: **trigger compaction
  explicitly**, **query compaction/derived-layer state**, and **read the
  original (raw) behind a compacted turn**. Retention settings ride the existing
  `UpdateConfig` / `GetConfig` RPCs. Where it fits, the same operations are
  exposed as MCP tools so non-gRPC consumers reach them too.
- Compaction runs **automatically in the background** in the agent *and* is
  **callable on demand** through the API — so a client can request it, but no
  client is required to drive it.
- A client's only job is presentation: render the derived (sent) view, the
  meter, "show original," and titles from agent-provided data (the CLI's `/c`).

## The tool-use constraint (what the cloud model rejects)

The Anthropic Messages API requires that **every `tool_use` block is answered by
a `tool_result` block with the matching id in the next message.** Orphan either
side → HTTP 400. `agent/history.go:repairPairing` already enforces this on every
request. Consequences for compaction:

- A `tool_use` and its `tool_result` move as a **pair**, or not at all. You
  cannot drop one without the other.
- **But** you can keep the pair's structure (the `tool_use` id, a `tool_result`
  with the matching id) and replace the `tool_result`'s **content** with a stub —
  `[elided: 4.2 KB read of foo.go]`. Pairing stays valid; the garbage
  evaporates. This is the single biggest, safest reclaim and needs no model.

Every compactor's output passes through `repairPairing` as a final invariant.

## 1. Shared substrate (algorithm-independent)

All candidate algorithms sit on the same lower layers, so the bake-off compares
*summarization strategy*, not incidental plumbing:

- **Mechanical pre-dedup / elision (deterministic, no model).** Before any
  summarization: collapse superseded tool results. The same file read N times
  keeps only the latest content; an identical re-run grep keeps only the latest;
  older results become one-line stubs (pairing preserved). Deterministic dedup is
  far more reliable than asking a model to dedupe, and it removes most of the
  bytes before the model ever runs.
- **Structured summaries (not prose).** A summary is a fixed-section object:
  `goal / key decisions / files touched (path → latest state) / open threads /
  current state`. Structured output merges deterministically (dedupe files by
  path, concatenate decision lists), degrades within per-section budgets, and is
  checkable in tests. Freeform prose blobs are where quality and dedup die.
- **Send-view assembly.** Given raw turns + the compacted artifacts, assemble the
  message array actually sent: `[structured summary preamble] + [compacted /
  elided mid-history] + [verbatim recent window]`, then `repairPairing`. The
  recent window is always sent verbatim; only older history is reduced.
- **Segmentation.** Split history into token-budgeted segments (~32–48k each) so
  each local-model call fits comfortably inside the 128k window.

## 2. `Compactor` interface + candidate algorithms

A pluggable interface (rough shape; finalized in 2a) lets the algorithm be
swapped and measured:

```
type Compactor interface {
    Name() string
    // Compact reduces raw turns into a send-view + persistable artifacts,
    // using summarize for any model-backed step. Pure w.r.t. live state.
    Compact(ctx, raw []Turn, summarize SummarizeFunc, budget Budget) (Result, error)
}
```

Candidates:

- **Rolling** — `summary_n = f(summary_{n-1}, chunk_n)`. Cheap, online, bounded
  window. Weakness: compounding loss (re-summarizes its own summary), recency
  bias, no global dedup.
- **Map-reduce** — summarize each chunk from raw independently (parallel), then
  reduce all chunk summaries together. No compounding loss on the map step; the
  reduce step sees everything → can dedupe globally. Weakness: the reduce can
  blow the local window with many chunks (needs hierarchical reduce); more passes.
- **Frozen-segment rolling-map-reduce** (favorite to beat) — map each completed
  segment **once from raw** and **freeze** it (never re-summarized → no
  compounding loss); **reduce periodically** over the frozen summaries (global
  dedup); **hierarchical reduce** only when the frozen set itself grows large.
  Online like rolling, global like map-reduce, lossless-of-its-own-output.

## 3. The bake-off (how the winner is chosen)

A metrics harness runs each compactor over the corpus and reports, per fixture
and aggregate:

| Metric | Meaning |
|---|---|
| Token-reduction ratio | sent tokens ÷ raw tokens |
| Anchor retention % | declared must-keep facts still present in the send-view |
| Dedup ratio | duplicate facts/tool-results collapsed |
| Pairing validity | send-view is always a valid API array (must be 100%) |
| Model-call count | local-model calls (cost/latency proxy) |

"Which algorithm" becomes a table of numbers. Structured summaries + mechanical
pre-dedup are the shared substrate, so the comparison is apples-to-apples.

## 4. The corpus (documented realistic patterns)

A set of fixtures, each a real `[]conversation.Turn` with valid `tool_use` /
`tool_result` pairs, each sized **past** the local window, each declaring its
must-keep facts (goal, specific paths, specific decisions) so anchor-retention is
checkable:

- Long debugging session with repeated reads of the same files.
- Research session with many web fetches.
- Refactor touching ~20 files.
- Q&A with little tool use (mostly prose).
- Same-file-read-5× (the dedup stressor).

Fixtures are documented (what pattern, what must survive) so the corpus is a
spec, not a black box.

## 5. Storage (Option 1: SQLite = raw source of truth)

- **Raw turns are never destroyed** (within retention). SQLite is already a
  durable, cold archive: we only `SELECT` what we send, so raw stays out of the
  hot path for free. No `/tmp` sidecar (temp dirs are fragile; the DB is not).
- **Compaction is a derived layer persisted alongside raw** (new columns/table):
  frozen segment summaries, elision stubs, the assembled send-view. The agent
  sends the derived view; `/c` "show original" reads raw; undo drops the derived
  layer.
- **Compaction output is a durable cache.** Once a span is compacted, the result
  is persisted and **never evicted while its raw input still exists, and never
  recomputed.** Keeping raw without its (smaller) compacted form would be the
  worst case — big *and* needing rework.

## 6. Retention (configurable; sensible defaults)

Layer-aware, two horizons, compacted ≥ raw:

| Layer | Default | Rationale |
|---|---|---|
| Raw tool-result bodies | 90 days | the expensive, reproducible 90% |
| Compacted layer (summaries, stubs) | 180 days | the cache; outlives raw |
| Keep-forever toggle | off | disables aging entirely |
| Per-conversation pin | — | never prune this one |

- Raw ages out at 90 days → its turns become their stubs/summary (still fully
  readable as the structured summary).
- The compacted layer ages out at 180 days.
- Past 180 days, the tiny **title + recap identity stub** (a few hundred bytes)
  survives until the user explicitly deletes the session, so `-r` history stays
  continuous (no holes) — hard-delete is reserved for an explicit user action.
- Power-user sizing that motivated this: raw is ~90% tool-result bytes,
  ~1–5 GB/year unbounded; the durable compressed layer is ~2–5% of that
  (~50–250 MB/year). Retention bounds raw to a window while keeping the cheap
  compressed memory long-term.
- All four knobs live in the existing config system (`cercano_config` /
  `config_editor`).

## 7. Live wiring (2b)

- **Trigger:** background, off the request path (like recap). Fires when the live
  context crosses a token threshold (a fraction of the cloud budget), debounced;
  runs concurrently so the result applies to the next request — "compacts while
  you do other stuff."
- **Persistence:** writes the derived layer (§5) so it is never recomputed.
- **`/c` integration:** `/c` shows the derived (sent) view — the truth of what
  the cloud sees, with an accurate meter — plus a "show original" affordance that
  reads raw. Coexists with the existing manual hard-delete.
- **Retention enforcement:** a periodic sweep applies §6.

## 8. Testing

- **Mechanical layer (deterministic, CI-fast):** segmentation, pre-dedup/elision,
  structured merge, send-view assembly, and pairing are tested exhaustively with
  a **fake summarizer** (no real model). Pairing validity is asserted on every
  produced send-view.
- **Model-fuzzy layer:** the summarize step is cornered by the corpus + property
  assertions (anchor retention, dedup, reduction, validity), with **optional
  real-local-model eyeball runs outside CI** for quality.
- The split keeps CI deterministic and fast (~80% of the system) while still
  measuring the fuzzy ~20%.

## Error / edge

| Case | Behavior |
|---|---|
| Local model fails mid-compaction | Keep the prior derived layer; retry later (never block a turn) |
| Send-view would be invalid | `repairPairing` guarantees validity as the last step |
| Raw pruned but model needs it | Reproducible — the agent re-runs the tool (re-reads the file) |
| Summary too aggressive | Tune; raw still present within its window for undo |
| Conversation pinned | Skip all pruning |

## Out of scope

The recap/title rung (sub-project 1, separate spec). A streaming cloud-side
compaction API. Cross-conversation memory. Model-based (vs mechanical) tool-result
dedup. Picking the algorithm before the bake-off measures it.

## Key file references

| Concern | Location |
|---|---|
| Tool-use pairing | `source/server/internal/agent/history.go` (`BuildLLMHistory`, `repairPairing`) |
| Turn storage + recap | `source/server/internal/conversation/store.go` (`Turn`, `GetTurns`, `DeleteTurns`, `UpdateRecap`) |
| Existing summarization plumbing | `source/server/internal/recap/recap.go` (`CompleteFunc`, debounced off-path generation) |
| Token sizing | `source/server/internal/contextmeter/` (128k default, `ModelMax`) |
| Config / settings | `cercano_config`, `source/clients/cli/internal/ui/config_editor.go` |
| `/c` viewer | `source/clients/cli/internal/ui/context_view.go` |
