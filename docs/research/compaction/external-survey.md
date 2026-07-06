# External survey: how other systems compact conversation context

Research supporting the compaction redesign. Question under study: **which
compaction techniques should be candidates in our comparative experiment
matrix?** (Benchmark set, metrics, and harness were agreed separately; this doc
feeds the "frames to compare" decision.)

## Sources and confidence

Primary sources (fetched directly, high confidence):

- Anthropic, *Effective context engineering for AI agents* — describes Claude
  Code's compaction, tool-result clearing, note-taking, sub-agents.
  https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents
- Aider source: `aider/history.py` (`ChatSummary`) and the repo-map docs.
  https://aider.chat/docs/repomap.html
- OpenHands agent SDK source: `openhands/sdk/context/condenser/` — README,
  `llm_summarizing_condenser.py`, and the production `summarizing_prompt.j2`.
  https://github.com/OpenHands/software-agent-sdk
- MemGPT paper (full text): *MemGPT: Towards LLMs as Operating Systems*,
  arXiv:2310.08560.

Secondary sources (local-model deep-research run, `scratch/research/compaction/`):
four arXiv memory papers (H-MEM, Membox, AgentSys, SafeHarbor) on hierarchical
memory. **Caveat:** these findings were extracted by the local model and the
papers have not been independently verified; treat as directional only. The
run's own gap analysis notes none of them evaluate extractive-vs-abstractive
trade-offs or coding-agent settings.

## Per-system notes

### Claude Code (Anthropic)

**Frame:** LLM paraphrase of message history + verbatim anchors + deterministic
elision as a separate, earlier lever.

- Compaction summarizes the message history near the context limit and
  reinitiates a fresh window with the summary **plus the five most recently
  accessed files verbatim**.
- The summary prompt is explicitly tuned to preserve "architectural decisions,
  unresolved bugs, and implementation details" and discard "redundant tool
  outputs or messages."
- Tuning method they recommend: **maximize recall first** on complex agent
  traces (capture everything load-bearing), then iterate on precision.
- **Tool-result clearing** is called out as "one of the safest, lightest-touch
  forms of compaction" — a deterministic, LLM-free lever that ships as a
  platform feature. This is exactly our `elide_tool_results` /
  `KeepLastNToolResults` path.
- Complementary techniques that reduce pressure on compaction: structured
  note-taking (NOTES.md / memory files persisted outside the window) and
  sub-agents that burn tens of thousands of tokens but return a 1–2k summary.

### OpenHands (All Hands AI)

**Frame:** rolling "forget the first half, replace with one summary event" over
an append-only event log. The most complete production design we found.

- **Nondestructive:** the event log is append-only. A condensation is a
  tombstone event (`Condensation`) recording which event IDs are forgotten and
  what summary replaces them. A `View` class applies tombstones at read time.
  The raw history is never lost.
- **Recursive:** summaries are themselves summarized in later condensations, so
  early context decays gradually rather than falling off a cliff.
- **Anchors:** `keep_first` (default 4) events are never condensed — the
  original user ask and setup stay verbatim. The recent tail is untouched.
- **Soft vs hard triggers:** size pressure is soft (skip condensation if it
  would violate message structure; retry next step). Explicit requests and
  token-limit overflow are hard (must condense now); the fallback is a
  hard context reset that summarizes everything, with progressive per-event
  truncation retries (scale event strings by 0.8, up to 5 attempts).
- **Guards:** minimum-progress check (condensation must forget ≥10% of the view
  or it's treated as an error); boundary-aware indices so tool-call/result
  pairs are never split.
- **Their prompt uses a taxonomy too** — USER_CONTEXT / TASK_TRACKING /
  COMPLETED / PENDING / CURRENT_STATE, plus CODE_STATE / TESTS / CHANGES / DEPS
  for code tasks — but it is *adaptive*: "Adapt tracking format to match the
  actual task type," "SKIP: tracking irrelevant details," and hard fidelity
  demands ("PRESERVE TASK IDs"). Sections are conditional, not slots to fill.

### Aider

**Frame:** head/tail split with weak-model paraphrase of the head; extractive
repo map for code context.

- `ChatSummary.summarize_real`: walk backward keeping the tail verbatim until
  it holds ~half the budget; align the split so the head ends on an assistant
  message; paraphrase-summarize the head with a cheaper model; if the result is
  still too big, recurse (depth ≤ 3), summarizing the summary.
- Only USER and ASSISTANT messages feed the summarizer — tool traffic is
  excluded from summary input entirely.
- Code context is handled by a separate, fully **extractive** structure: the
  repo map (graph-ranked signatures under a fixed token budget). Nothing about
  code is paraphrased; it is quoted from source.

### MemGPT / Letta

**Frame:** memory hierarchy with agent-directed movement between tiers; the
summary is an index, not the record.

- Context = system instructions + self-edited "working context" + FIFO message
  queue whose head slot holds a **recursive summary** of evicted messages.
- Everything evicted goes to **recall storage** (a searchable message DB) —
  the agent can page original messages back into context via function calls.
  Hallucination in the summary is recoverable because ground truth remains
  retrievable.
- **Memory-pressure warnings:** at ~70% capacity the system warns the agent so
  it can proactively copy important facts to working context or archival
  storage *before* eviction — importance triage happens ahead of loss.
- Evaluated wins come mostly from retrieval (deep memory retrieval, multi-hop
  doc QA), not from summary quality.

### Academic cluster (secondary, unverified)

Hierarchical memory (facts → summaries → abstractions) with per-layer retention
policies; topic-threading (Membox "Topic Loom") to keep conversational arcs
together instead of flattening per-utterance. Directionally consistent with the
practitioner systems; no coding-agent or extractive-vs-abstractive evidence.

## Cross-cutting patterns

Every production system paraphrases — nobody ships pure extraction. But nobody
ships *bare* paraphrase either. Four mitigations recur:

1. **Verbatim tail + verbatim anchors.** Recent turns are never summarized
   (all four systems). OpenHands pins the first events; Claude Code pins the
   five most recent files.
2. **The raw log survives compaction.** OpenHands tombstones over an
   append-only log; MemGPT keeps everything in searchable recall storage.
   Summary errors are recoverable, so the fidelity bar the summary itself must
   clear is lower.
3. **Deterministic elision before LLM summarization.** Tool-result clearing
   (Claude Code), excluding tool traffic from summary input (Aider), truncating
   oversized events (OpenHands). The LLM sees less noise, and the cheapest
   savings never touch a model.
4. **Recall-first prompt tuning with concrete fidelity demands.** "Preserve
   exact task IDs" (OpenHands), "preserve architectural decisions, unresolved
   bugs" (Claude Code). Adaptive sections beat rigid slots: OpenHands makes
   sections conditional on content, which plausibly reduces the
   fill-the-empty-slot hallucination we observed.

Where Cercano stands against that list: we have #3 (elision, keep-last-K) and a
recall-first-ish prompt after the fidelity fix. We do not have #1 in a strong
form (worth confirming exactly what stays verbatim around the frozen range),
and we have the storage for #2 (every turn is in conversations.db) but no
retrieval path — a summary error today is unrecoverable at inference time.

## Candidate frames for the experiment matrix

Recommended set, refined from the original A–D sketch by the evidence above:

- **A. Baseline — current paraphrase + fixed taxonomy** (post-fidelity-fix).
  Already instrumented via compaction-repro/proposal-repro.
- **B. Adaptive-taxonomy paraphrase (OpenHands-style).** Conditional sections,
  hard "preserve exact identifiers/IDs" rules, verbatim keep-first anchors and
  tail, recursive re-summarization of prior summaries. Nearest-neighbor
  improvement to what we have; cheap to build from the existing summarizer.
- **C. Elision-first + verbatim tail.** No LLM in the load-bearing path:
  tool-result elision + keep-last-K + head/tail split where only genuinely
  redundant middle prose is dropped (not paraphrased). Summarize only as a
  last resort under hard token pressure. Zero hallucination surface by
  construction; the question is whether compression is sufficient — our
  elide-probe numbers say byte-identical dedup alone is not (0.4%).
- **D. Extractive quoting.** LLM selects load-bearing spans quoted verbatim;
  output is verifiable (every span must be a substring of the source — a
  mechanical hallucination check). No production precedent found, which makes
  it the novel candidate worth testing rather than assuming.
- **E. Retrieval-backed summary (MemGPT-style).** Keep a (possibly worse)
  summary but add an agent tool to page original turns back from
  conversations.db on demand. Changes the failure economics — summary drift
  becomes recoverable — rather than the summary quality. Orthogonal to A–D and
  composable with any of them; in the matrix it tests "does recoverability
  matter more than fidelity?"

Suggested measurement note: frames C and D admit *mechanical* hallucination
metrics (substring verification); A and B need the anchor/tell methodology from
proposal-repro. E needs a task-completion-style probe (can the agent recover a
dropped proposal when asked?), not just a static fidelity score.
