# Caveats — read before trusting this research

This folder is the durably-persisted output of a `research` run (2026-07-25) that
recovered the task-model investigation lost when the 2026-07-23 session died. It is
useful, but it is **not** clean signal. Read it with the following corrections in mind.

## 1. The precise numbers in `synthesis.md` are fabricated. Ignore them.

The local synthesis model invented authoritative-looking metrics that correspond to
**no real published benchmark**. Do not cite or design against any of these:

- "TodoWrite executes at 14.7 steps/second with 99.2% accuracy"
- "reducing initialization from 3.2s to 0.4s, skipping list creation in 83% of cases"
- "Conductor achieves 85% accuracy, delivers plans in under 2.3 seconds"
- "1.2 MB cache", "68% faster", "12 predefined node types", "JSON schema v2.4"

These are hallucinated. The *directional* claims they dress up (TodoWrite is flat and
fast; Conductor decomposes hierarchically) are real; the quantities are not.

## 2. Four off-topic sources were pruned (2026-07-25).

The retriever mis-matched on the words "task / scheduling / performance" and pulled in
unrelated arXiv papers. These were deleted from `findings/`, and the orphaned
`references/` directory (Google Scholar author pages, a settings page, a Cyrillic
Google landing page — all reference-chasing artifacts of the deleted papers) was removed:

- 01 — Genetic Algorithms for Software Model Refactoring
- 02 — A Survey of Multi-Agent Deep Reinforcement Learning with Communication
- 03 — GA and LSH in Multiprocessor Job Scheduling
- 16 — Parallel CPU/GPU Algorithms for Unified Gas-Kinetic Scheme

`synthesis.md` still references some of these in its "Recommended Reading Order"
(items 5, 13, 14, 15) and retrofits them as evidence ("GA-inspired dependency
sequencing", "MADRL-like coordination"). **That framing is invalid** — disregard it.

## 3. What IS credible (the on-topic subset).

The GitHub / DeepWiki findings are real and checkable:

- **Superpowers** (findings 04, 12, 18) — flat, markdown task files (`plans/active/`,
  `docs/plans/task.md`), verification hooks. No built-in deep hierarchy.
- **Claude Code** (findings 06, 07, 08, 11) — two distinct mechanisms: **TodoWrite**
  (flat, ephemeral, session-scoped) vs. a heavier **Task / Tasks-API** path.
- **Conductor** (findings 13, 14) — genuine recursive `track > plan > phase > task`
  decomposition with role assignment and GitHub integration.
- **copilot-orchestra, hyperpowers, aider** (findings 15, 17, 10, 09) — corroborate
  that most tools are file/markdown-based and flat-to-shallow.

## 4. The real gap the research could NOT fill.

There is almost no public documentation of the **on-disk data structure, persistence
format, or UI surfacing** of these task models. That is precisely the core of roadmap
item #3 (schema + persistence + client surfacing) — so it is a *design* question for us,
not something more web search will answer. Do not spend budget on the "deep" follow-up
suggested at the bottom of `synthesis.md`; it would chase the same thin surface and
likely hallucinate more.
