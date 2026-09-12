# Task routing caller inventory

Source audit on 2026-09-12, starting from main 974432c1. This is a scoped source inventory, not end-to-end runtime verification. Production code remains unchanged.

## Producers

A delegated non-test Go search for `dispatch.Spec{` returned 12 constructors in 10 files. Line numbers below refer to that baseline. RoutingTask is the stable routing class; Task is the free-form instruction. Never substitute one for the other.

| Producer | Current routing and quality | Required treatment |
| --- | --- | --- |
| builtins/dispatch_cap.go:89 | TaskDispatch, explicit difficulty mapped by tierForDispatch; omitted difficulty inherits saved dispatch quality | Add optional validated class selector; omission remains Default dispatch. Explicit quality must not choose destination. |
| builtins/review.go:62,70 | No RoutingTask; RoleMain and everyday for one-shot and agentic review | Both paths carry Review; remove unintended fixed quality. |
| builtins/web_common.go:52,78 | Budget and execution both omit RoutingTask; RoleCoproc, wrapper tier/model | Both paths carry Research with identical class, quality and override intent. |
| builtins/gitflow_land.go:203 | Conflict-review gate has no RoutingTask; RoleMain/everyday; source gitflow:land-review | Preserve Git land identity, not independent Review; deterministic mechanics stay unchanged. |
| builtins/coproc.go:21 | No RoutingTask; RoleCoproc/fast_light_text | Explicit co-processor exclusion; preserve model selection. |
| builtins/local_offload.go:69 | No RoutingTask; RoleCoproc/everyday with optional model override | Explicit local-offload exclusion; do not move to Default dispatch. |
| server/watchdog_wire.go:73 | No RoutingTask; RoleCoproc with resolved watchdog model override | Superseded: classify as Watchdog, Local/Standard default; remove model override. |
| worker/watchdog.go:68 | No RoutingTask; RoleCoproc with resolved watchdog model override | Same normal Watchdog classification in worker; remove model override. |
| agent/agent.go:540 | processCoproc; RoleCoproc, no tier, forwards model override, thinking flag and conversation ID | Co-processor exclusion at the producer, not a general rule that every RoleCoproc request is excluded. |
| hostsvc/persistence/persistence.go:1410 | Next-prompt suggestion; RoleCoproc/fast_light_text, source suggest_next_prompt | Existing background co-processor behavior; preserve under the co-processor exclusion rather than introducing a new task class. |

Paths above are relative to source/server/internal. Research callers are builtins/web_research.go:82 and web_deep_research.go:98. Both currently supply fast_light_text to the shared wrapper; deep research also forwards a.Model. Budget and execution use the same wrapper source/work directory/tier/model fields. CallQuery disables thinking, while ordinary Call does not; class changes must not change this behavior.

## Shared execution and aliases

- dispatch.Spec.RoutingTask currently documents empty as legacy role policy. dispatch.Engine.resolve checks only nonemptiness, not validity. Nonempty requests consult candidate TaskFor (preferred) or engine taskAssignment, then select a destination and its model. Empty requests select by locus/role. Direct inspection plus fresh tests confirm that unknown classes can execute on Primary and ordinary missing-class requests ignore Default dispatch.
- Target, PreparedTarget and Dispatch share resolve. The agentic runner receives the complete Spec, not a reconstruction. startup_fallback skips explicit task routing; changing class defaults requires preserving exclusions deliberately, without adding fallback edges.
- Worker buildWorkerToolSvc installs the shared hostsvc/tools service with SetEngine and the same capability stack. There is no separate worker dispatch.Spec constructor in worker_dispatch.go. Tests must still establish runtime parity; source sharing alone does not prove it.
- Host tools keep spec.Task as persisted user text and tool-loop input. RoutingTask must remain distinct metadata throughout wrappers.
- builtins/builtins.go:114 registers workflow as a CapabilitySynonyms alias of dispatch. The optional class must survive this same capability path; do not add a parallel workflow schema or parser.
- Generic dispatch currently advertises task prose, optional light/standard/deep tier and cwd/path, but no class selector. Reconnaissance, Mechanical development, Investigation and Implementation are caller-selected semantic classes, not distinct hard-coded constructors found in this inventory. Their primary entry point is generic dispatch plus first-party agent guidance. Do not infer them from prompt words or quality.

## Guidance targets

- docs/agent/self-dev.md:25 and :70–83 describe locus/role and requested quality as dispatch routing, omitting existing TaskDispatch destination selection. Update to class-selected destination plus independent quality, while retaining the excluded co-processor policy.
- docs/agent/sub_agents/README.md contains generic task examples and a Spec-field inventory around :184 without RoutingTask. Add class examples and explain Default dispatch compatibility, explicit class validation and exclusions.
- Tool schema quality guidance currently recommends light for recon/tracing/extraction without a class selector. Update alongside the selector so readers do not confuse quality with placement.

Some initial delegated searches were truncated. This record uses subsequent bounded searches and direct inspection to close the producer, alias, shared-worker and guidance gaps; it does not claim a complete audit of all inference APIs or every documentation mention. Phase 4 must re-audit producer classification after changes, and Phase 6 must verify propagation with fake providers.

## Approved amendment — 2026-09-12

The watchdog rows now use normal task routing (Local/Standard by default) in host and worker, without legacy model pins. Earlier role/exclusion statements are historical. Deprecated co-processor migration/removal remains pending; no new exclusion mechanism is authorized.

## Final producer audit — 2026-09-12

Historical exclusions above are superseded: runTextAnalysis backs summarize/extract/classify/explain with Reconnaissance; next-action extraction and MCP project-context extraction use Reconnaissance. MCP documentation generation selects Mechanical development. Generic dispatch/workflow exposes the metadata-validated class selector and preserves it (including Investigation/Implementation) across wrappers and agentic execution; omitted class always uses Default dispatch. Review uses Review in both modes. Research budgets, normal inference and query generation use Research, preserving query-only DisableThinking. Git land retains Git land through its existing review gate. Host and worker Watchdog use Watchdog/Local/Standard unless changed in Routing.

The legacy ProcessRequest coproc flag is deprecated and translates to ordinary Default dispatch. Explicit routing_task crosses the wire for known first-party one-shot producers. The unused grpcModelCaller was removed. Only the explicitly local capability carries LocalOffload; no role/source-text inference and no Watchdog/co-processor exemption remain. Re-audited production dispatch.Spec construction across source/server with grep; all remaining producers are described here. Historical model-choice and role notes above are not current behavior.
