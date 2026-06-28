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
   and retires `dispatch.Tool`.
   → [`capability-architecture/design.md`](capability-architecture/design.md)

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

5. **Subagent execution engine** — Spawnable sub-agent loops for plan execution, with
   smart model routing (a lighter/faster model for simple tasks), protocol-aware
   context setup, and adversarial review. Aliased to the `workflow` name so host models
   that reach for "the workflow tool" find it. Depends on Tier 0 and Tier 1.

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

## Status

- Tier 0 designs written; foundation is the current focus.
- Tiers 1 and 2 are not yet specced.
</content>
</invoke>
