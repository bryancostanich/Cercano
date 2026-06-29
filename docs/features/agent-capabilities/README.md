# Agent Capabilities

A multi-part upgrade to Cercano's standalone agent behaviors and capabilities. Most
of these capabilities are designed to be usable **both** when Cercano runs as a
standalone agent **and** when it runs as a plugin (MCP co-processor) inside a host
agent like Claude Code.

This README is the umbrella roadmap. Each sub-project gets its own spec → plan →
implementation cycle under its own directory here.

## Decomposition

The work breaks into 8 sub-projects across 3 dependency tiers. Build order follows
the tiers; within a tier, pieces are largely independent.

### Tier 0 — Foundation (everything sits on these)

1. **Capability architecture + migration** — How a capability is written once and
   exposed both as a standalone built-in tool and as an MCP plugin tool. Today the
   standalone tools (`agenttools.Tool`), the dispatch loop's tools (`dispatch.Tool`),
   and the MCP handlers are three parallel implementations with no shared core. This
   sub-project introduces one `Capability` model, migrates all existing tools onto it,
   and retires `dispatch.Tool`. **Tasks 1–10 built.**
   → [`capability-architecture/design.md`](capability-architecture/design.md)

   **1b. Dispatch engine (folded into 0a).** Co-processor commands and subagent calls
   are one primitive ("delegated model work"). Adds the one-shot/agentic `Dispatch`
   primitive, a provider-boundary cloud-vs-local usage layer, surface-aware
   project-context injection, the co-processor capabilities, the `dispatch`/`workflow`
   capability, and a `review` capability. **Pulls sub-project #5 (subagent engine)
   forward into 0a.** Design complete; implementation plan pending; protocol/watchdog
   pieces depend on #2.
   → [`dispatch-engine/design.md`](dispatch-engine/design.md)

2. **Steering & protocol substrate** — Plain-English steering plus the workflow
   protocol library (ported from the `hardwAIr_hckr` "Dave" plugin and the generic
   `khalkulo/workflow` protocols). Defines how protocols reach the model: an always-on
   steering block in the standalone system prompt, an on-demand protocol pull, and the
   skill catalog for hosts. Includes the post-MVP watchdog design.
   → [`steering-protocol-substrate/design.md`](steering-protocol-substrate/design.md)

### Tier 1 — Building blocks

3. **Task model + tracking + client surfacing** — A plan/task data model, tracking
   during execution, and streaming task state to clients so they can display it (e.g.
   a right-hand pane in the CLI when wide enough, or a bottom strip otherwise).

4. **Git workflow tools** — Algorithm-driven git workflows: worktree creation, branch
   management, and the rebase-onto-main-then-merge-back flow. LLM only as backup where
   no deterministic path exists.

### Tier 2 — Modes & engines

5. **Subagent execution engine** — **Pulled forward into 0a as the dispatch engine
   (see 1b above).** Spawnable sub-agent loops, smart model routing (via the routing
   seam + locus mode), protocol-aware context, adversarial review (the `review`
   capability), and the `workflow` alias. No longer a separate Tier-2 item.

6. **Brainstorming mode** — An interactive design dialogue, modeled on (and improving)
   the Superpowers brainstorming skill. Depends on the steering substrate.

7. **Planning mode** — A full-featured planning mode: the Conductor plan format with
   Superpowers-style execution. Depends on the task model, subagent engine, and
   brainstorming.

8. **Autonomous mode** — A protocol/mode (not a single tool) that wires the decision
   protocol and other workflow pieces together for unattended runs. Depends on the
   steering substrate, subagent engine, and planning.

## Dependency summary

```
Tier 0:  [1 Capability arch] ── [2 Steering substrate]
                │                      │
Tier 1:  [3 Tasks]  [4 Git workflows]  │
                │        │             │
Tier 2:  [5 Subagent engine] ←─────────┘
            │     │
         [7 Planning] ← [6 Brainstorming]
            │
         [8 Autonomous mode]
```

## Source material

- **`hardwAIr_hckr`** ("Dave") — a working Claude Code plugin (Go MCP server + skill
  library) whose job is injecting engineering-discipline protocols. Its `core/` skills
  (design-decisions, systematic-debugging, verification-strategy, compute-before-simulate)
  are finished. Decision: **Cercano becomes the home** for protocol-driven discipline;
  Dave's protocol library is ported into Cercano and Dave is retired/superseded.
- **`khalkulo/workflow/`** — source protocols. Two are fully generic and reusable:
  `design_decisions.md` (a 7-step decision protocol) and the debug-loop skeleton in
  `RTL_debugging_protocol.md` (STRIP DOWN → OBSERVE → REASON → PREDICT → PROBE →
  REFERENCE → FIX). The rest are chip-design-specific.
- **Superpowers** (`github.com/obra/superpowers`) and **Conductor**
  (`github.com/gemini-cli-extensions/conductor`) — references for the brainstorming,
  planning, and subagent-execution sub-projects (Tier 2). Conductor's plan format is
  preferred; Superpowers' execution model is preferred.

## Design notes (captured during planning, for the sub-projects they affect)

- **Planning mode (#7):** at the end of generating a plan, do NOT just point the user at
  the plan file to read. Offer to produce a concise, heavily bullet-pointed version of
  the plan for review, and generate it on request. (Pointing someone to "go read the
  plan" is a poor handoff; a tight bulleted digest respects their time.)

## Candidate capabilities (not yet scheduled)

- **`cercano_compact` capability** — expose the existing, well-developed `internal/compaction`
  subsystem (rolling / map-reduce / summarizer / dedup / elision) as a capability: feed it
  text or a transcript, get back a compacted summary. Built on the dispatch engine's
  capability model (one-shot mode), so it lights up on both surfaces. It would **not**
  auto-manage a host's context window (a host owns its own context; capabilities return
  data), but a host could call it deliberately to compact large material locally. Candidate
  once the dispatch engine lands.

## Status

- **0a capability foundation (Tasks 1–10): built** in this worktree, each task reviewed
  clean. Not yet merged to main. 15 file/git tools unified as capabilities on both
  surfaces; `dispatch.Tool` retired; `InvokeCapability` + MCP adapter live.
- **0a dispatch engine (1b): design complete + implementation plan written**
  (`dispatch-engine/design.md`, `dispatch-engine/plan.md`). Scope decision: **unify
  everything first** — migrate co-processor work and the agentic loop onto `llm.Provider`,
  build the `Dispatch` primitive on that one boundary (15 tasks / 8 phases). Recon found
  the main agent's `RunToolLoop` is already a reusable, fully-parameterized provider tool
  loop, so Agentic dispatch reuses it. Not yet built. Supersedes the old Task 11.
- **0b steering & protocol substrate: BUILT** (worktree branch, whole-branch review = ready
  to merge; not merged). Protocol library + steering block + `get_protocol` + `protocols sync`.
- Tier 1 (task model, git workflows) and remaining Tier 2 (brainstorming, planning,
  autonomous) not yet specced.
</content>
</invoke>
