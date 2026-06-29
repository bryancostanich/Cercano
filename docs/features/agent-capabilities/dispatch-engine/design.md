# Dispatch Engine — Design (in progress)

**Status:** Active brainstorm. Decisions below are settled with the user; open forks are listed at the end. Supersedes Spec 0a's "Task 11 / co-processor migration" — the co-processor work is now part of this.

**Relationship to other specs:**
- Sits on the **Spec 0a** capability foundation (Capability/Registry/Services, agent + MCP adapters, `InvokeCapability`).
- Pulls forward and merges the original Tier-2 **subagent execution engine** (#5 in the [roadmap](../README.md)) with the co-processor migration. They are the same primitive.

## Core concept: delegated model work

A **co-processor command** and a **subagent call** are the same primitive — hand a unit of work to a model and get a result back — differing only in configuration:

| | Co-processor (one-shot) | Subagent (agentic) |
|---|---|---|
| Prompt | fixed template | open-ended task |
| Tools / loop | none, one-shot | tools + bounded loop |
| Model | routed (usually local) | routed (light vs heavy) |
| Context | project context | project context + workflow protocols |
| Result | text/structured | result + maybe artifacts/review |

Co-processor = the degenerate case (fixed prompt, no tools, one-shot). Subagent = the general case. `cercano_dispatch` / `dispatch.Loop` is the existing agentic prototype (now on the capability registry after 0a Task 7).

**The dispatch primitive is the single seam** where model routing, project-context injection, workflow-protocol injection, and usage recording all attach — uniformly, for co-processor and subagent callers alike.

## Settled decisions

1. **Usage recorded at the provider boundary.** Every `llm.Provider` Chat/StreamChat emits a usage event `{tool/source, model, cloud|local, in/out tokens, session}` to the shared telemetry store. One chokepoint captures standalone tool-loop turns *and* dispatched work. Cloud-vs-local is known for free (each provider is one or the other). Caveat to verify in the plan: the co-processor execution path must route through a provider, not the engine side channel.
   - The **context-meter stays separate** — it measures live context-window occupancy per conversation, a different job. We are unifying *cost-telemetry coverage*, not merging the two systems.
   - Host-reported cloud tokens (`cloudTokenFields` / `cercano_submit_usage`) remain an MCP-surface input feeding the same store.

2. **Project-context injection: per-capability flag + inject in the dispatch primitive.** A capability declares whether it wants project context (`fetch` = no; `summarize`/`extract`/`classify`/`explain`/`research`/`document` = yes). The shared dispatch primitive prepends the `.cercano/context.md` digest to its one-shot prompt when the flag is set and a project dir is present; no-op otherwise. The standalone loop's system-prompt `<project-context>` block is unchanged.
   - **No double-injection risk** (this was a phantom): a co-processor capability invoked inside the standalone loop makes its *own separate* one-shot model call via the dispatch primitive — that call has no system block, so prepending is correct; the loop's own calls are distinct calls that get context via the system block. Two different calls, neither injected twice. (Strike any earlier double-inject warning.)

3. **Subagent engine pulled forward.** Design the full dispatch primitive now — agentic loop, model routing, interaction — not deferred. Co-processor is its one-shot configuration.

4. **Interaction model splits by surface (driven by the MCP-callback constraint).** Some co-processor commands were designed to send status back and interact with the driving agent, but major MCP hosts (Claude Code, etc.) don't reliably process MCP callbacks/status notifications. Therefore:
   - **Standalone (Cercano drives the loop):** full rich dispatch — agentic loop, live status to the CLI, interaction (a subagent can surface a question/decision/permission up to the main loop), cancel. Reliable because Cercano owns both ends. **This is the real home of the subagent engine.**
   - **MCP plugin (host drives):** dispatch is **request/response**, non-interactive. Best-effort progress notifications are emitted for hosts that show them, but nothing depends on them. No callbacks, no interaction.
   - Principle: **interaction is a standalone capability; the plugin surface is non-interactive dispatch.**

5. **MCP long-running comms: synchronous + best-effort progress (now).** The MCP tool call blocks until the dispatch finishes and returns the full result; progress notifications are emitted but nothing depends on them. Matches what hosts actually support.
   - **Future upgrade (documented, not built now):** a "sync default + async job+poll opt-in" mode — dispatch returns a job id, host polls a status/result tool — for genuinely long work that risks host-side tool-call timeouts. Captured here so it isn't lost.

## Open forks (to resolve next)

- **Model routing policy** — how the model is chosen for a dispatched unit (light vs heavy; local vs cloud escalation); how explicit vs automatic.
- **Workflow-protocol injection** — which protocols get injected into subagent context, and how selected (ties to Spec 0b).
- **Adversarial review** — in scope now or a later layer.
- **`workflow` alias** — aliasing the subagent entry to `workflow` (Claude reaches for "the workflow tool").
- **Parallel fan-out** — multiple subagents concurrently.
- **Dispatch primitive API shape** — what config it takes; where it lives (agent-side).
- **Subagent tool access** — which capabilities a subagent is granted (subset of the registry).
- **Standalone invocation surface** — how the main agent/user launches a subagent (tool call, slash command, both).

## Doc-structure note
Scope has grown from "extend 0a" toward a distinct engine spec. Revisit whether this stays folded into 0a or becomes its own spec once the open forks are resolved.
</content>
