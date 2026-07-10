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
   forward into 0a.** **BUILT** (see Status).
   → [`dispatch-engine/design.md`](dispatch-engine/design.md)

2. **Steering & protocol substrate** — Plain-English steering plus the workflow
   protocol library (ported from the `hardwAIr_hckr` "Dave" plugin and the generic
   `khalkulo/workflow` protocols). Defines how protocols reach the model: an always-on
   steering block in the standalone system prompt, an on-demand protocol pull, and the
   skill catalog for hosts. Part C — the **watchdog** protocol supervisor — is **BUILT**
   (see Status).
   → [`steering-protocol-substrate/design.md`](steering-protocol-substrate/design.md) ·
   [`watchdog/design.md`](watchdog/design.md)

### Tier 1 — Building blocks

3. **Task model + tracking + client surfacing** — A plan/task data model, tracking
   during execution, and streaming task state to clients so they can display it (e.g.
   a right-hand pane in the CLI when wide enough, or a bottom strip otherwise).

4. **Git workflow tools** — Algorithm-driven git workflows: worktree creation, branch
   management, and the rebase-onto-main-then-merge-back flow. LLM only as backup where
   no deterministic path exists. **BUILT** (see Status).
   → [`git-workflows/design.md`](git-workflows/design.md)

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

Everything below is **BUILT and merged to `main`**.

- **0a capability foundation: DONE.** One `Capability` model; 15 file/git tools unified as
  capabilities on both surfaces; `dispatch.Tool` retired; `InvokeCapability` + MCP adapter live.
- **0b steering & protocol substrate: DONE.** Protocol library + always-on steering block +
  `get_protocol` capability + `cercano protocols sync` SKILL.md generation.
- **0a dispatch engine (1b): DONE.** Co-processor work and the agentic loop both run on
  `llm.Provider` through one `Dispatch` primitive with a single seam for routing
  (`dispatch.Select`), project-context injection (`ContextAware`), and usage
  (`usage.RecordingProvider`). OneShot + Agentic modes; Agentic reuses the main agent's
  `RunToolLoop`. Co-processor execution migrated; `summarize`/`extract`/`classify`/`explain`
  are now capabilities on both surfaces; added `dispatch`/`workflow` and `review` capabilities;
  least-privilege subagent grants. Legacy `ModelProvider` coproc path + engine-based
  `dispatch.Loop` retired. Supersedes the old Task 11. Coproc telemetry later consolidated
  at the provider boundary (the Item-1 usage seam: `Spec.RecordUsage` + savings carried).
  → [`dispatch-engine/plan.md`](dispatch-engine/plan.md) ·
  [`dispatch-engine/implementation-notes.md`](dispatch-engine/implementation-notes.md)
  (as-built deviations, telemetry decision, follow-ons)
- **#4 git workflow tools: DONE.** Pure-git deterministic engine (`internal/gitflow`) +
  capabilities: worktree lifecycle, `checkpoint` (commit a solved unit; never trunk, never
  push), the land flow (rebase → verify → ff-only, with conflict-marker guards and safety
  refs), squash/recover/bisect, and a review-gated auto-land. Trunk name is a parameter,
  never assumed.
  → [`git-workflows/design.md`](git-workflows/design.md) · [`git-workflows/plan.md`](git-workflows/plan.md)
- **0b Part C watchdog: DONE** (five increments, all under [`watchdog/`](watchdog/)):
  the protocol supervisor — a fast-model gate on the main tool loop (`WatchdogGate` +
  `WatchdogTurnEnd` seams), challenge/justify/escalate state machine, fail-open everywhere,
  default-OFF. Checks are named after the canonical protocols where possible:
  `systematic-debugging`, `design-decisions`, `commit-checkpoint` (semantic work-boundary
  commit nudge), `plain-english` (turn-end register check that reopens the turn for a
  rewrite), `worktree-first`, and `follow-through`. Client visibility (a `WatchdogEvent` stream message rendered
  as CLI callouts + a debug echo of the watchdog/main exchange) and full runtime control
  from the settings page (Development Tools → Watchdog: enable, echo, mode, per-check
  toggles, escalate-after), applied live via a lock-free rebuild.
  → [`watchdog/design.md`](watchdog/design.md) (core) · `increment-2-*` (visibility +
  commit-checkpoint) · `developer-settings-*` / `watchdog-settings-completeness-*`
  (settings) · `turn-end-plain-english-*` (turn-end gate) · `polish-*` (increment C)

**Remaining — not yet specced** (roadmap bullets only; each gets its own design → plan when
started): **#3 task model + client surfacing** (Tier 1), and Tier 2's **#6 brainstorming**,
**#7 planning**, **#8 autonomous mode**. The watchdog's own deferred items (the matrix-router
"fast" model class, per-conversation echo isolation, a turn-end human-confirm on escalate)
are logged in the watchdog docs as follow-ons, not scheduled.
</content>
</invoke>
