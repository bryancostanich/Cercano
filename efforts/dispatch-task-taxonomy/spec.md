# Task-class routing and dedicated Routing configuration

Status: user-approved specification and execution plan, including destination redirects and the subsequent Watchdog Local/Standard amendment.

## Requirements confirmed by the user
- Work on main in Cercano; do not create a worktree.
- Task class selects a configurable destination. Quality independently selects a model tier within that destination. Quality must never determine destination.
- Provide a dedicated /config Routing page containing both destination setup and task assignments. Remove these controls from Cloud settings rather than duplicating them.
- Destination setup covers Primary, Secondary, their optional backup profiles, and the existing Local setup relevant to routing. Preserve existing runtime/model ownership; do not invent a cloud-profile binding for Local.
- Cloud remains the profile/credential/model-choice editor; Local Models remains local model management.
- Move existing chat routing onto the Routing page alongside dispatch task assignments.
- Provide a configurable Default dispatch bucket for calls without a task class; its unset destination defaults to Secondary. Preserve existing saved dispatch assignment as this bucket and existing default quality behavior.
- Explicit task classes should cover essentially all first-party model-assisted dispatch paths. The default bucket is compatibility coverage, not the normal first-party route.
- Audit callers, wrappers, schemas, and agent guidance so task identity is preserved through host and worker execution. Do not infer task class from quality or prompt keyword heuristics.
- Preserve independent fallback policy; this change does not authorize new destination fallback edges.
- Direct operations that do not need inference remain direct tools. Do not introduce model calls for deterministic Git commands.
- Git land is a dedicated task class for the existing dispatched landing workflow, with an independently configurable destination defaulting to Local and default quality Premium.
- Git land mechanics are explicitly outside scope: preserve existing conflict handling, stopping/reporting behavior, test and review gates, and continuation behavior. Do not define new stopping criteria, regeneration rules, retry limits, or escalation behavior in this effort.
- Treat Git land as one dispatched task for this effort. Decomposing it into smaller tasks is a future direction, not scope authorized here.
- Watchdog is a normal dispatch task class, defaulting to Local/Standard. It uses the common assignment, redirect, model selection and permitted fallback rules, with no watchdog routing exemption or legacy watchdog.model precedence. Checks, timeout/cancellation, escalation and audit behavior are unchanged.
- Co-processor is deprecated. Migrate still-reachable callers to appropriate task classes or remove them in follow-up work; do not introduce a new exemption mechanism. Explicit local-offload semantics remain preserved pending further resolution.

## Confirmed taxonomy and defaults
The user approved Reconnaissance and Mechanical development as distinct task classes, not a combined Development support class. The explicit dispatch classes are Reconnaissance, Mechanical development, Investigation, Implementation, Review, Research, Git land, and Watchdog, alongside Chat and Default dispatch assignments.

The user approved these stable keys and task-specific defaults:

- Reconnaissance (`reconnaissance`): Local destination, Light quality.
- Mechanical development (`mechanical_development`): Local destination, Standard quality.
- Investigation (`investigation`): Secondary destination, Premium quality.
- Implementation (`implementation`): Secondary destination, Premium quality.
- Review (`review`): Secondary destination, Premium quality.
- Research (`research`): Secondary destination, Premium quality.
- Git land (`git_land`): Local destination, Premium quality.
- Watchdog (`watchdog`): Local destination, Standard quality.

Each class has independently configurable destination and default quality. These are initial defaults, not mandatory assignments. Chat and Default dispatch retain existing saved assignments and default-quality behavior. Changing quality never changes destination.

Task-specific quality defaults were chosen over Premium for every class to reflect task demands, with potentially lower cost and latency for lighter work. Actual savings and quality depend on the configured models; users can raise a class's quality independently.

| Axis | Task-specific quality defaults (approved) | Premium for every class |
| --- | --- | --- |
| Defaults | Reconnaissance Light; Mechanical development and Watchdog Standard; others Premium | All eight Premium |
| Complexity | Eight default entries and corresponding tests | Same eight entries and tests |
| Risk | Lighter models may underperform | Potentially unnecessary cost or latency |
| Destination independence | Preserved | Preserved |

## Confirmed destination redirects
- The Routing page provides a global redirect setting for Secondary: use its own configuration (default), redirect to Primary, or redirect to Local.
- The Routing page provides a global redirect setting for Local: use its own configuration (default), redirect to Secondary, or redirect to Primary. No Primary redirect control is authorized.
- Resolve task class and quality first, then follow destination redirects to the final destination. Preserve task identity and resolved quality throughout; do not inherit another task's quality default or reclassify the task.
- Select the model for that quality from the final destination. Use that destination's configuration, including its profile and backup configuration when it is a cloud destination.
- Example: Investigation assigned to Secondary/Premium, with Secondary redirected to Local, uses Local's Premium model while remaining Investigation/Premium.
- Follow redirect chains to their final destination. Reject cycles, including Secondary → Local → Secondary, rather than looping or silently choosing a destination.
- Redirects are explicit routing choices, not failure-triggered fallback. They do not authorize new fallback edges. Watchdog follows the same redirects as other classes; the earlier watchdog exclusion is superseded. Deprecated co-processor migration/removal and explicit local-offload treatment remain follow-up work.
- Missing redirect settings preserve existing destination behavior. Persist settings and carry them through host/worker routing alongside task assignments.

## Approved amendment — 2026-09-12

The user replaced watchdog special-case routing with the normal task-class contract and explicitly selected Local/Standard, not Light. Earlier watchdog-exclusion statements in historical audits are superseded. Legacy watchdog.model is retained in storage/transport for now but no longer selects watchdog dispatch models. Destination redirect runtime integration is still pending for all classes, including Watchdog; classification alone does not establish that verification.

## Read-only caller inventory — 2026-09-11

Historical baseline; watchdog exclusions and co-processor-preservation recommendations below are superseded by the approved amendment above.

This is a first-pass source inventory, not runtime verification or a complete audit of every inference entry point. No sub-agent dispatch tool was available in the planning session, so focused reads and searches were used directly.

- `internal/capabilities/builtins/dispatch_cap.go`: generic agentic dispatch accepts task prose and quality but no task class; it always sets `RoutingTask: TaskDispatch`. This is the principal caller-selected classification boundary.
- `internal/capabilities/builtins/review.go`: both one-shot and agentic review omit RoutingTask and hard-code everyday quality with RoleMain. Both should carry Review identity.
- `internal/capabilities/builtins/gitflow_land.go`: the existing conflict-resolution review gate uses a one-shot model call without RoutingTask. The initial proposal to classify landing by internal step is superseded by the user-confirmed dedicated Git land class, defaulting to Local for the single dispatched workflow. Trace this nested review call during implementation planning to avoid accidentally overriding the landing assignment or introducing destination escalation. Deterministic Git operations remain unchanged.
- `internal/capabilities/builtins/web_common.go`: research Budget and execution construct separate specs, both lacking RoutingTask. They must carry the same class and model-selection intent. `web_research.go` and `web_deep_research.go` construct this shared wrapper.
- `internal/server/watchdog_wire.go`: watchdog one-shot calls omit RoutingTask and preserve an explicit watchdog model override. A matching producer was located in `internal/worker/watchdog.go`; its exact behavior still needs inspection before proposing override precedence changes.
- `internal/capabilities/builtins/coproc.go`: fixed co-processor prompts use RoleCoproc and fast_light_text with no RoutingTask. Preserve the approved co-processor exclusion explicitly rather than silently moving these into Default dispatch.
- `internal/capabilities/builtins/local_offload.go`: generic local offload also uses RoleCoproc without RoutingTask. Its relationship to the co-processor exclusion needs explicit scope confirmation; do not silently reroute a tool advertised as local.
- `internal/dispatch/engine.go`: RoutingTask already exists in Spec, with empty currently preserving legacy role policy. Target, PreparedTarget, and Dispatch share resolution. Changing every empty Spec globally would affect the excluded callers, so compatibility handling must distinguish ordinary unclassified dispatch from explicitly preserved legacy behavior.
- `pkg/config/routing.go`: only chat and dispatch are valid saved task keys today. Unknown task lookup starts with Primary/Premium even though saved unknown keys are rejected. New classes require shared validation and defaults, not just new labels.
- `internal/routingwire/routing.go` and `pkg/agentclient/routing.go`: searches locate string-keyed assignment map transport. Exact end-to-end class validation and snapshot handling remain implementation-planning checks.
- `source/clients/cli/internal/ui/config_tabs.go`: seven current tabs, no Routing tab. `cloud_routing.go` currently renders destination bindings and only chat/dispatch assignments. Navigation, dirty-state ownership, and Save/Discard must move with the controls.
- `docs/agent/self-dev.md` and `docs/agent/sub_agents/README.md`: dispatch guidance and examples need explicit class selection; old role/quality-based explanations must not remain authoritative.

Approval record: the user approved the overall spec, including rejection of unknown explicit classes, then confirmed destination redirects with preserved task identity and quality, final-destination model/profile/backup selection, chain resolution, and cycle rejection. The approved implementation plan is being executed. The later Watchdog amendment above supersedes the original exclusion requirement.

## Acceptance
- Routing UI has destination setup and task assignment sections, with Save/Discard semantics and navigation/unsaved-draft coverage; Cloud no longer renders routing controls.
- Classified requests use their saved class destination and quality defaults; explicit quality changes cannot change destination.
- Missing classes use the configurable Default dispatch bucket, defaulting to Secondary; existing saved dispatch settings survive.
- First-party inference dispatch producers have explicit classes or documented, narrowly justified exceptions. Generic wrappers require or propagate class rather than blanket-stamping a default.
- Unknown explicit classes are rejected rather than silently treated as missing.
- Redirect tests cover each supported direction, no-redirect defaults, multi-hop resolution, cycle rejection, persistence/transport/snapshots, and Routing UI Save/Discard. Host and worker use the final destination's models and cloud backup configuration while preserving task identity and quality; excluded callers retain existing behavior.
- Config persistence, client/server transport, worker snapshots, host/worker resolution, and end-to-end fake-provider integration preserve class identity.
- Tests prove ordinary first-party classified paths do not fall through to Default dispatch, including when the bucket is configured to a different destination.
- No live inference or broad regression results may be claimed without running them.

## Implementation record — 2026-09-12

The approved plan and Watchdog amendment are implemented; see verification.md for precise test scope and limitations. Co-processor helpers were migrated to normal task routing (Reconnaissance for narrow text analysis); the deprecated one-shot wire flag is compatibility-only and uses Default dispatch. Explicit local-offload intent is preserved without a Watchdog exemption. Routing is now a separate page from Cloud, covering all shared TaskDefinitions. Historical pending-approval and exclusion observations above do not describe the final implementation.
