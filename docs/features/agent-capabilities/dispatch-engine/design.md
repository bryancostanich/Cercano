# Dispatch Engine — Design (in progress)

**Status:** Design complete on the core architecture (settled decisions below); feature-set forks drafted + confirmed. **Folded into Spec 0a** as extended phases (user decision) — this is part of the 0a deliverable, not a separate spec. **Supersedes 0a's old "Task 11 / co-processor migration"** — that work is now this engine.

**Relationship to other specs:**
- Part of **Spec 0a** (extended): sits on the same capability foundation (Capability/Registry/Services, agent + MCP adapters, `InvokeCapability`) built in 0a Tasks 1–10.
- Pulls the original Tier-2 **subagent execution engine** (#5 in the [roadmap](../README.md)) forward into 0a and merges it with the co-processor migration. They are the same primitive.
- Depends on **Spec 0b** for protocol injection + the watchdog (review enforcement).

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
   - **RESOLVED (caveat verified, consequential decision):** Recon confirmed three parallel model-call boundaries today — the main agent on `llm.Provider`, co-processor commands on the legacy `ModelProvider.Process()` (langchaingo, via gRPC `ProcessRequest{Coproc:true}`→`agent.processCoproc`), and `dispatch.Loop` on `engine.InferenceEngine.ChatWithTools`. The single-seam premise required choosing how far to unify. **Decision: "unify everything first"** (clean sweep, consistent with the 0a migrate-everything choice) — migrate both co-processor work and the agentic loop onto `llm.Provider`, then build Dispatch on that one boundary so usage/routing/context attach in exactly one place. Both migration targets exist already (`llm/anthropic` cloud + `llm/ollama` local, with tool-call support), so the unification is feasible. The implementation plan is scoped accordingly.
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

6. **Routing is a consumed seam governed by locus mode; not owned by this engine.**
   - The dispatch primitive asks a **router seam** "for this work, under the current locus mode, which provider + model?" — it does not implement the routing intelligence.
   - **Locus mode is the hard governor.** `internal/locus/locus.go`: `cloud_only`/`local_only` forbid the other tier; `cloud_primary`/`local_primary` set preferred + fallback with escalation when cross-allowed. `mode.Main()` vs `mode.Coproc()` already return per-path tier policy; co-processor work already routes via `mode.Coproc()` in `processCoproc`. Whatever the router proposes is bounded by locus.
   - **Today's seam implementation:** reuse the existing locus-driven selection + `SmartRouter`; the dispatch primitive just unifies the call site so one-shot and agentic dispatch route consistently.
   - **Caller hints** (a role/tier preference, or `ModelOverride`) are advisory inputs the router *may* honor within locus bounds — never an override of locus.
   - **Future upgrade (documented, not built now):** an embedded small-model router that analyzes the prompt and references a **model×capability matrix** to choose the target. The matrix is greenfield today (only `llm.Capabilities` + a binary code/research split in `internal/research/modelcheck.go`). When it lands, only the seam's *implementation* changes — the dispatch engine does not.

7. **Cloud dispatch uses provider-neutral cost/quality profiles.** When the locus allows cloud dispatch, model choice should not be a raw vendor model name and should not be tied to tool permissions. Use three user-facing profiles:
   - **economy** — cheap and fast; good for read-only exploration, summaries, catalog checks, and mechanical edits that are easy to verify.
   - **standard** — the default for normal coding, documentation, bounded multi-file edits, and test repair.
   - **premium** — strongest allowed model for hard design, ambiguous debugging, adversarial review, security-sensitive review, or changes where a wrong answer is expensive.

   The stable rule is: **use the least expensive configured model that is likely to succeed, but do not go below the task's risk level.** A subagent with write tools can still use `economy` when the edit is mechanical and tests check it; a read-only subagent can still need `premium` when the judgment is hard. Permissions answer "what may this subagent do?" Profiles answer "how strong and expensive should the model be?"

   Profiles are provider-neutral. Each provider maps `economy` / `standard` / `premium` to its own model names, and `premium` means "the strongest configured model for this provider," not a promise that all vendors' premium models are equal. Example shape:

   ```yaml
   model_profiles:
     cloud:
       default_provider: anthropic
       default_profile: standard

       providers:
         anthropic:
           economy:
             model: claude-3-5-haiku-latest
           standard:
             model: claude-sonnet-4-20250514
           premium:
             model: claude-opus-4-20250514

         openai:
           economy:
             model: gpt-4.1-mini
           standard:
             model: gpt-4.1
           premium:
             model: o3

         google:
           economy:
             model: gemini-2.5-flash
           standard:
             model: gemini-2.5-pro
           premium:
             model: gemini-2.5-pro
   ```

   Dispatch may accept explicit provider/profile hints or `auto`. In `auto`, the router can ask the current cloud model to choose from the configured allowlist using the task, tools, expected output, and risk. The model chooses a profile, not an arbitrary model name; Cercano validates the choice against config and locus. Dispatch results should report the resolved route plainly, for example: `cloud/anthropic/economy: claude-3-5-haiku-latest; reason: read-only catalog check with low risk and easy verification`.

   **As-built (implemented).** §7 is now implemented, with refinements settled during the build:

   - **The cost table is keyed by vendor, and each cloud profile names its vendor.** `CloudProfile` gained a `provider:` field (anthropic|openai|google|…); the table lives at `model_profiles.cloud.providers.<vendor>.{economy,standard,premium}.model`. The profile is the runtime selection unit and is *not* 1:1 with a vendor (two profiles can share a vendor, or share a route), so the profile declares which vendor's lineup it draws from. Empty `provider:` is inferred from the profile's flavor/backend at resolution time.
   - **Capability tiers map onto cost tiers on the cloud side.** Internal lanes ask for a capability tier (`most_capable`/`everyday`/`fast_light`/`fast_light_text`); on the cloud side that maps to `premium`/`standard`/`economy`. The provider-blind `models.tiers.*.cloud` slots are retired — load-tolerant, no longer read. Open/local models keep their capability-tier `open` slots unchanged.
   - **Resolution + guard.** Cloud selection = active profile → its vendor → `providers[vendor][costTier].model`, falling back to the profile's own `model` on any miss (vendor-correct by definition). A loud warning fires when a resolved model is neither in the vendor's table nor the profile's own model — surfacing the silent cross-vendor rejection that motivated the work (an Anthropic id sent to the Codex route).

## Subagent engine — feature design (DRAFT, unconfirmed)

Drafted by the assistant; not yet user-confirmed. Consequential items are pulled out to a decision-matrix review with the user (see "Adversarial review" below).

### Dispatch primitive (draft)
- One agent-side entry: `Dispatch(ctx, DispatchSpec) (DispatchResult, error)`.
- `DispatchSpec = { Mode: OneShot|Agentic; Prompt/Task; Tools []string (capability names); WantsProjectContext bool; Protocols (auto|explicit); ModelHint; ConversationID }`.
- **OneShot** → a single routed model call + context injection + usage recording. Co-processor capabilities call this with their fixed template.
- **Agentic** → a bounded tool loop over the granted capability subset (reuses the existing tool-loop / `dispatch.Loop` machinery), routed via the seam, with live status events standalone / synchronous result over MCP.
- Routing, project-context injection, protocol injection, and usage recording all attach here (the single seam from the settled decisions).

### Standalone invocation (draft)
- The main agent launches a subagent by calling a built-in **agentic-dispatch capability** (`dispatch`), **aliased to `workflow`** so host models that reach for "the workflow tool" find it. (Alias: confirmed-cheap, yes.)
- Optional `/dispatch` / `/workflow` slash command later; not required for v1.
- MCP: the existing `cercano_dispatch` tool maps to Agentic mode (sync + best-effort progress per the comms decision).

### Subagent tool access (draft — recommendation; say if you want this matrixed)
- The spec declares an **explicit capability-name subset**. Default grant = R-tier inspect/read capabilities; W/X granted only when explicitly listed.
- **Never exceeds the parent session's permission mode** — a subagent cannot escalate beyond what the parent could do. Bypass-mode parents can grant W/X; strict/permissive parents gate the subagent's W/X the same way.

### Workflow-protocol injection (draft seam; specifics pend Spec 0b)
- Agentic dispatch composes the subagent's system prompt from: persona + the **0b steering block** + task-triggered protocol bodies (via 0b `get_protocol`) + project context (if flagged).
- Seam only for now; concrete protocol selection waits until the 0b protocol substrate exists.

### Parallel fan-out (draft — later)
- The primitive is single-dispatch; an orchestrator launches N concurrently under a **bounded concurrency cap** (modeled on superpowers `dispatching-parallel-agents`).
- Build sequential first; parallel as a fast-follow.

### Adversarial review — RESOLVED: review as its own capability (Option 3)

Decided via the khalkulo decision framework. The fork was *how* review attaches to agentic dispatch:
1. Caller-driven orchestration (engine dumb; orchestrator composes review).
2. Baked into agentic Dispatch (a `Review` config inside the primitive).
3. **Review as its own capability** (chosen).

**Decision:** `review`/`verify` is a **first-class capability** built on Dispatch (a refute-style prompt + a verdict schema), invoked by orchestrators/protocols. The engine stays a clean primitive (rejects Option 2's coupling). Enforcement that it actually *runs* comes from the **0b protocols + watchdog** (e.g. the watchdog requires `review` before a commit/merge for risk classes), not from baking review into Dispatch.

**Trigger policy:** protocol/watchdog-required for risk classes; opt-in otherwise. Not always-on (wasteful), not model's-whim (skip-risk).

#### How superpowers does this, and how we improve it
(Per the original ask: "lean heavily on superpowers and improve it." First-hand from running these skills.)

Superpowers is mechanically closest to **Option 1**, not 3: the subagent-spawn primitive is **dumb**, and review is an **orchestration recipe encoded in skills + prompt templates** that the controller follows —
- `subagent-driven-development`: controller dispatches a fresh **implementer** per task → implementer self-reviews + commits → controller builds a diff package → dispatches a separate **task-reviewer** subagent returning spec-compliance + quality verdicts (Critical/Important/Minor) → dispatches **fix** subagents for Critical/Important → re-reviews → final **whole-branch reviewer** (`requesting-code-review`).
- Adversarial patterns: N independent **skeptics** each prompted to *refute*, kill on majority-refute; perspective-diverse lenses; judge panels; loop-until-dry.
- The reviewer is "just another subagent with a reviewer prompt"; review is composed, not baked into the engine — the philosophy Options 1 and 3 share (and why we reject 2).

**Superpowers' weaknesses → our improvements:**
1. **Review is a soft skill instruction**, enforced by prose ("Never skip task review", "RED FLAGS", "you MUST") — it relies on the agent *choosing* to follow the skill, i.e. the rationalize-and-skip failure mode. **Our improvement:** `review` is a **named capability** (discoverable, reusable on both surfaces) and the **0b watchdog hard-enforces** it for risk classes (no `review` before commit → blocked). Enforcement moves from "please follow the skill" to a structural gate.
2. **Review topology is hand-rolled** in the controller each time. **Our improvement:** as a capability with a verdict schema, review is consistent and composable — an orchestrator or protocol calls `review`, optionally fanning out N skeptics with diverse lenses.

Net: keep superpowers' correct instinct (dumb engine, composed review), but elevate review to a **capability** and move enforcement to the **watchdog**.

## Doc-structure note (resolved)
Folded into Spec 0a as extended phases (user decision). 0a's Tasks 1–10 (capability foundation + 15-tool migration + dispatch consolidation + `InvokeCapability` + MCP adapter) are done; the old Task 11 is superseded by this engine. The dispatch-engine phases (one-shot/agentic Dispatch primitive, provider-boundary usage layer, project-context flag, co-processor capabilities on the primitive, `dispatch`/`workflow` capability, `review` capability) still need an implementation plan written before building — and the protocol/watchdog pieces depend on 0b landing.
</content>
